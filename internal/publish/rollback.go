// SPDX-License-Identifier: Apache-2.0

package publish

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rasonyang/aicc-knowledge/internal/domain"
	"github.com/rasonyang/aicc-knowledge/internal/store/queries"
)

// RollbackResult describes a successful Rollback.
type RollbackResult struct {
	Language domain.Language
	// From is the publication that was LIVE and is SUPERSEDED now.
	From uuid.UUID
	// To is the publication that is LIVE now.
	To uuid.UUID
	// Rebuilt is true when the target's index was gone or did not hold the
	// target's documents, so it was rebuilt from publication_items.
	Rebuilt  bool
	Items    int
	Pruned   []uuid.UUID
	Duration time.Duration
}

// Rollback makes a SUPERSEDED publication LIVE again. With to nil the target
// is the most recently superseded publication of the language. If the target's
// content_uid names an index that holds exactly its documents, that index is
// swapped in; otherwise a new index is built from publication_items (re-embedding
// the snapshotted questions) and swapped in. The states flip in one transaction
// after the swap task succeeded.
func (p *Publisher) Rollback(ctx context.Context, lang domain.Language, to *uuid.UUID) (RollbackResult, error) {
	start := time.Now()
	res := RollbackResult{Language: lang}
	unlock, err := p.lock(ctx, lang)
	if err != nil {
		return res, err
	}
	defer unlock()
	res, err = p.rollback(ctx, lang, to)
	res.Duration = time.Since(start)
	if p.Metrics != nil {
		outcome := "LIVE"
		if err != nil {
			outcome = "FAILED"
		}
		p.Metrics.ObserveRollback(ctx, string(lang), outcome)
	}
	return res, err
}

func (p *Publisher) rollback(ctx context.Context, lang domain.Language, to *uuid.UUID) (RollbackResult, error) {
	res := RollbackResult{Language: lang}
	q := p.Store.Queries
	cur, err := q.GetLivePublication(ctx, string(lang))
	if errors.Is(err, pgx.ErrNoRows) {
		return res, fail(CodeNoLivePublication, "there is no LIVE publication of "+string(lang)+" to roll back from", nil)
	}
	if err != nil {
		return res, fail(CodeDatabase, "read the live publication", err)
	}

	var target queries.Publication
	if to != nil {
		if target, err = q.GetPublication(ctx, *to); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return res, fail(CodeRollbackTargetBad, "no publication "+to.String(), nil)
			}
			return res, fail(CodeDatabase, "read the target publication", err)
		}
		if target.Language != string(lang) {
			return res, fail(CodeRollbackTargetBad, fmt.Sprintf("publication %s is %s, not %s", target.ID, target.Language, lang), nil)
		}
		if _, err := domain.PublicationState(target.State).Transition(domain.PublicationLive); err != nil || target.State != string(domain.PublicationSuperseded) {
			return res, fail(CodeRollbackTargetBad, fmt.Sprintf("publication %s is %s; only a SUPERSEDED publication can be rolled back to", target.ID, target.State), err)
		}
	} else {
		sup, err := q.ListSupersededPublications(ctx, string(lang))
		if err != nil {
			return res, fail(CodeDatabase, "list superseded publications", err)
		}
		if len(sup) == 0 {
			return res, fail(CodeNoRollbackTarget, "there is no SUPERSEDED publication of "+string(lang), nil)
		}
		target = sup[0]
	}
	res.From, res.To = cur.ID, target.ID

	items, err := q.ListPublicationItems(ctx, target.ID)
	if err != nil {
		return res, fail(CodeDatabase, "read the publication items", err)
	}
	res.Items = len(items)
	live := p.LiveUID(lang)

	source := ""
	if target.ContentUid != nil && *target.ContentUid != live {
		exists, err := p.Meili.IndexExists(ctx, *target.ContentUid)
		if err != nil {
			return res, fail(CodeIndexVerifyFailed, "look up the retained index", err)
		}
		if exists {
			ok, detail, err := p.holds(ctx, *target.ContentUid, items)
			if err != nil {
				return res, fail(CodeIndexVerifyFailed, "read the retained index", err)
			}
			if ok {
				source = *target.ContentUid
			} else {
				p.log().Warn("retained index does not hold the publication; rebuilding", "uid", *target.ContentUid, "detail", detail)
			}
		}
	}
	if source == "" {
		id, err := uuid.NewV7()
		if err != nil {
			return res, fail(CodeDatabase, "generate an index name", err)
		}
		source = p.stagingUID(lang, id, "rb")
		res.Rebuilt = true
		if err := p.build(ctx, source, target.ID, items); err != nil {
			p.dropIndex(source)
			var pe *Error
			if errors.As(err, &pe) {
				return res, pe
			}
			return res, fail(CodeIndexBuildFailed, "rebuild the index", err)
		}
	}
	// A failure from here on leaves a rebuilt index behind only if the swap
	// itself did not happen; clean it up.
	cleanupRebuilt := func() {
		if res.Rebuilt {
			p.dropIndex(source)
		}
	}
	if h := p.Hooks.BeforeSwap; h != nil {
		if err := h(ctx, source); err != nil {
			cleanupRebuilt()
			return res, fail(CodeHookFailed, "BeforeSwap hook", err)
		}
	}

	displaced, err := p.swapIn(ctx, source, live)
	if err != nil {
		cleanupRebuilt()
		return res, fail(CodeSwapFailed, "swap the target index with the live index", err)
	}
	if h := p.Hooks.AfterSwap; h != nil {
		if err := h(ctx); err != nil {
			p.undoSwap(source, live, displaced)
			cleanupRebuilt()
			return res, fail(CodeStateCommitFailed, "AfterSwap hook", err)
		}
	}
	if err := p.commitRollback(ctx, lang, cur.ID, target.ID, displaced); err != nil {
		p.undoSwap(source, live, displaced)
		cleanupRebuilt()
		return res, fail(CodeStateCommitFailed, "record the rollback", err)
	}
	if res.Rebuilt && target.ContentUid != nil && *target.ContentUid != live && *target.ContentUid != displaced {
		p.dropIndex(*target.ContentUid) // the stale claim the rebuild replaced
	}
	res.Pruned = p.retain(ctx, lang)
	p.log().Info("rolled back", "language", lang, "from", cur.ID, "to", target.ID, "rebuilt", res.Rebuilt)
	return res, nil
}

// commitRollback applies LIVE -> SUPERSEDED and SUPERSEDED -> LIVE in one
// transaction. The content_uid claims are cleared before they are reassigned
// because the unique index would reject two claimants, even briefly.
func (p *Publisher) commitRollback(ctx context.Context, lang domain.Language, curID, targetID uuid.UUID, displaced string) error {
	tx, err := p.Store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	q := p.Store.Queries.WithTx(tx)

	cur, err := q.LockPublication(ctx, curID)
	if err != nil {
		return err
	}
	target, err := q.LockPublication(ctx, targetID)
	if err != nil {
		return err
	}
	if _, err := domain.PublicationState(cur.State).Transition(domain.PublicationSuperseded); err != nil {
		return fmt.Errorf("the current publication changed while rolling back: %w", err)
	}
	if _, err := domain.PublicationState(target.State).Transition(domain.PublicationLive); err != nil {
		return fmt.Errorf("the target publication changed while rolling back: %w", err)
	}
	if err := q.SetPublicationContentUID(ctx, queries.SetPublicationContentUIDParams{ID: target.ID}); err != nil {
		return err
	}
	var uidPtr *string
	if displaced != "" {
		uidPtr = &displaced
	}
	if n, err := q.SupersedePublication(ctx, queries.SupersedePublicationParams{ID: cur.ID, ContentUid: uidPtr}); err != nil || n != 1 {
		return fmt.Errorf("supersede the current publication: rows=%d err=%v", n, err)
	}
	live := p.LiveUID(lang)
	if n, err := q.PromotePublication(ctx, queries.PromotePublicationParams{ID: target.ID, FromState: string(domain.PublicationSuperseded), ContentUid: &live}); err != nil || n != 1 {
		return fmt.Errorf("promote the target publication: rows=%d err=%v", n, err)
	}
	return tx.Commit(ctx)
}
