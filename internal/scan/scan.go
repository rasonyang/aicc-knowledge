// SPDX-License-Identifier: Apache-2.0

// Package scan finds new, changed and deleted source files in S3 and records
// them as immutable file versions in PostgreSQL.
//
// One scan lists the bucket and prefix with ListObjectsV2 (paginated) and
// compares each object's size, LastModified and ETag with what the previous
// scan observed (source_files.observed_*). Any difference means "download and
// hash": the SHA-256 of the bytes is the identity of content, and an ETag is
// only a change signal (a multipart upload's ETag is not a content hash).
//
//   - same hash as the current version: no new version, only the newly observed
//     metadata is recorded so the next scan does not download again;
//   - different hash: the current version is superseded, its candidates become
//     STALE, and a new version (version_no+1) is inserted, DISCOVERED with a
//     PARSE job for .docx, .xlsx and *.facts.yaml, or UNSUPPORTED with
//     parse_error_code UNSUPPORTED_FORMAT for everything else, or UNSUPPORTED
//     with OBJECT_TOO_LARGE (no job) when the object is larger than the
//     configured cap;
//   - every version that stops being current (superseded or removed) also
//     clears the import pointer of the fact tables it fed (they become
//     UNAVAILABLE until a new import succeeds);
//   - a key that is no longer listed: its current version becomes REMOVED
//     (terminal, superseded_at set) and its candidates STALE. If the key
//     comes back it gets a new version, even with the old content.
//
// Which objects are ignored, as if they did not exist: keys ending in "/"
// (console "directory" markers) and zero-byte objects. Ignoring means absence:
// if an object that already has a version is emptied, it is treated as
// removed.
//
// The size cap (KB_S3_MAX_OBJECT_BYTES) does not hide an object. The bytes are
// streamed through SHA-256 without buffering whatever their number, and an
// object over the cap is versioned as UNSUPPORTED with parse_error_code
// OBJECT_TOO_LARGE and no PARSE job, so an existing file that grows past the
// cap keeps a truthful history instead of silently becoming REMOVED. The
// cap bounds what the parsers hold in memory, not what the scan reads.
//
// Each object's change is one transaction. Scans are serialized by a
// PostgreSQL advisory lock, so two scans can never interleave. The removal
// pass runs only after the whole listing succeeded: a failed listing never
// removes anything.
package scan

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/rasonyang/aicc-knowledge/internal/domain"
	"github.com/rasonyang/aicc-knowledge/internal/jobs"
	"github.com/rasonyang/aicc-knowledge/internal/obs"
	"github.com/rasonyang/aicc-knowledge/internal/s3store"
	"github.com/rasonyang/aicc-knowledge/internal/store"
	"github.com/rasonyang/aicc-knowledge/internal/store/queries"
)

// ParseErrorUnsupportedFormat is stored in file_versions.parse_error_code of
// an UNSUPPORTED version.
const ParseErrorUnsupportedFormat = "UNSUPPORTED_FORMAT"

// ParseErrorObjectTooLarge is stored in file_versions.parse_error_code of an
// UNSUPPORTED version whose object is larger than the configured cap.
const ParseErrorObjectTooLarge = "OBJECT_TOO_LARGE"

// Reasons stored in fact_tables.last_error_code when a source stops being live.
const (
	FactSourceSuperseded = "SOURCE_SUPERSEDED"
	FactSourceRemoved    = "SOURCE_REMOVED"
)

// lockKey is the advisory lock that serializes scans ("KNSC").
const lockKey int64 = 0x4b4e5343

// Outcome names the metric label values of kb_scan_objects_total.
const (
	OutcomeNewVersion   = "NEW_VERSION"
	OutcomeUnchanged    = "UNCHANGED"
	OutcomeMetadataOnly = "METADATA_ONLY"
	OutcomeRemoved      = "REMOVED"
	OutcomeUnsupported  = "UNSUPPORTED"
	OutcomeIgnored      = "IGNORED"
	OutcomeOversize     = "OVERSIZE"
	OutcomeError        = "ERROR"
)

// Format says how a key is processed.
type Format int

// Formats.
const (
	FormatUnsupported Format = iota
	FormatDocx
	FormatXlsx
	FormatFacts
)

// Classify maps an object key to its format by extension, case-insensitively.
// ".doc", ".xls", ".pdf" and everything else are unsupported.
func Classify(key string) Format {
	lower := strings.ToLower(key)
	switch {
	case strings.HasSuffix(lower, ".facts.yaml"):
		return FormatFacts
	case path.Ext(lower) == ".docx":
		return FormatDocx
	case path.Ext(lower) == ".xlsx":
		return FormatXlsx
	}
	return FormatUnsupported
}

// Summary counts what one scan did. Seen counts eligible objects; every one of
// them lands in exactly one of Unchanged, MetadataOnly, NewVersions or Errors.
// Unsupported is the subset of NewVersions that are UNSUPPORTED, and Oversize
// the subset of those that exceed the size cap (OBJECT_TOO_LARGE).
type Summary struct {
	Seen            int
	NewVersions     int
	Unchanged       int
	MetadataOnly    int
	Removed         int
	Unsupported     int
	Ignored         int
	Oversize        int
	Errors          int
	JobsEnqueued    int
	StaleCandidates int64
	BytesDownloaded int64
}

// Scanner runs scans against one bucket.
type Scanner struct {
	Store *store.Store
	S3    *s3store.Client
	// Metrics is optional.
	Metrics *obs.Metrics
	Log     *slog.Logger
	// MaxObjectBytes is the largest object that gets a PARSE job; a larger
	// one is versioned UNSUPPORTED/OBJECT_TOO_LARGE.
	MaxObjectBytes int64
	// PageSize is the ListObjectsV2 page size; zero means the S3 maximum.
	PageSize int32
}

// stateRow is what the previous scans left for one key.
type stateRow struct {
	queries.ListScanStateRow
}

func (r *stateRow) hasCurrent() bool { return r.VersionID != nil }

func (r *stateRow) matches(o s3store.Object) bool {
	return r.hasCurrent() && r.ObservedSizeBytes != nil && *r.ObservedSizeBytes == o.Size &&
		r.ObservedEtag != nil && *r.ObservedEtag == o.ETag &&
		r.ObservedLastModifiedAt.Valid && r.ObservedLastModifiedAt.Time.Equal(o.LastModified.Truncate(time.Microsecond))
}

// Run performs one full scan. Per-object failures are counted in
// Summary.Errors and logged, and the scan goes on; the returned error is for
// failures of the scan itself (lock, listing, context). After a listing error
// nothing is removed.
func (s *Scanner) Run(ctx context.Context) (Summary, error) {
	start := time.Now()
	var sum Summary
	log := s.Log
	if log == nil {
		log = slog.Default()
	}

	conn, err := s.Store.Pool.Acquire(ctx)
	if err != nil {
		return sum, fmt.Errorf("acquire scan lock connection: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", lockKey); err != nil {
		return sum, fmt.Errorf("take scan lock: %w", err)
	}
	defer func() {
		uctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.Exec(uctx, "SELECT pg_advisory_unlock($1)", lockKey)
	}()

	rows, err := s.Store.Queries.ListScanState(ctx, s.S3.Bucket)
	if err != nil {
		return sum, fmt.Errorf("load scan state: %w", err)
	}
	state := make(map[string]*stateRow, len(rows))
	for _, r := range rows {
		state[r.ObjectKey] = &stateRow{r}
	}

	eligible := map[string]bool{}
	err = s.S3.List(ctx, s.PageSize, func(page []s3store.Object) error {
		for _, o := range page {
			if strings.HasSuffix(o.Key, "/") || o.Size == 0 {
				sum.Ignored++
				continue
			}
			eligible[o.Key] = true
			sum.Seen++
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := s.processObject(ctx, o, state[o.Key], &sum); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				sum.Errors++
				log.Error("scan object failed", "key", o.Key, "error", err)
			}
		}
		return nil
	})
	if err != nil {
		s.observe(ctx, sum, start)
		return sum, err
	}

	for key, r := range state {
		if eligible[key] || !r.hasCurrent() || !strings.HasPrefix(key, s.S3.Prefix) {
			continue
		}
		if err := s.removeObject(ctx, r, &sum); err != nil {
			if ctx.Err() != nil {
				s.observe(ctx, sum, start)
				return sum, ctx.Err()
			}
			sum.Errors++
			log.Error("remove object failed", "key", key, "error", err)
		}
	}
	s.observe(ctx, sum, start)
	return sum, nil
}

func (s *Scanner) observe(ctx context.Context, sum Summary, start time.Time) {
	if s.Metrics == nil {
		return
	}
	for outcome, n := range map[string]int{
		OutcomeNewVersion: sum.NewVersions, OutcomeUnchanged: sum.Unchanged, OutcomeMetadataOnly: sum.MetadataOnly,
		OutcomeRemoved: sum.Removed, OutcomeUnsupported: sum.Unsupported, OutcomeIgnored: sum.Ignored,
		OutcomeOversize: sum.Oversize, OutcomeError: sum.Errors,
	} {
		s.Metrics.ObserveScanOutcome(ctx, outcome, int64(n))
	}
	s.Metrics.ObserveScan(ctx, sum.BytesDownloaded, time.Since(start))
}

func (s *Scanner) processObject(ctx context.Context, o s3store.Object, st *stateRow, sum *Summary) error {
	if st != nil && st.matches(o) {
		sum.Unchanged++
		return nil
	}
	body, err := s.S3.Open(ctx, o.Key)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(h, body) // streamed: memory use does not depend on the object size
	_ = body.Close()
	sum.BytesDownloaded += n
	if err != nil {
		return fmt.Errorf("download %s: %w", o.Key, err)
	}
	if n == 0 {
		return fmt.Errorf("object %s was empty when downloaded", o.Key)
	}
	meta := body.Meta
	meta.Size = n // the bytes actually read are the truth
	if meta.LastModified.IsZero() {
		meta.LastModified = o.LastModified
	}
	return s.apply(ctx, o.Key, h.Sum(nil), meta, sum)
}

// apply records a downloaded object in one transaction.
func (s *Scanner) apply(ctx context.Context, key string, hash []byte, meta s3store.Object, sum *Summary) error {
	var res Summary
	err := pgx.BeginFunc(ctx, s.Store.Pool, func(tx pgx.Tx) error {
		res = Summary{}
		q := s.Store.Queries.WithTx(tx)
		sf, err := q.UpsertSourceFile(ctx, queries.UpsertSourceFileParams{Bucket: s.S3.Bucket, ObjectKey: key})
		if err != nil {
			return fmt.Errorf("upsert source file: %w", err)
		}
		cur, err := q.GetCurrentFileVersion(ctx, sf.ID)
		hasCur := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("load current version: %w", err)
		}
		observed := queries.UpdateObservedMetadataParams{
			ID: sf.ID, ObservedSizeBytes: &meta.Size, ObservedEtag: &meta.ETag,
			ObservedLastModifiedAt: pgtype.Timestamptz{Time: meta.LastModified, Valid: true},
		}

		if hasCur && bytes.Equal(cur.SHA256, hash) {
			res.MetadataOnly++
			return q.UpdateObservedMetadata(ctx, observed)
		}
		if hasCur {
			if _, err := q.SupersedeFileVersion(ctx, cur.ID); err != nil {
				return fmt.Errorf("supersede version: %w", err)
			}
			n, err := q.MarkCandidatesStale(ctx, cur.ID)
			if err != nil {
				return fmt.Errorf("stale candidates: %w", err)
			}
			res.StaleCandidates += n
			if _, err := q.ClearFactTablesForVersion(ctx, queries.ClearFactTablesForVersionParams{VersionID: &cur.ID, ErrorCode: FactSourceSuperseded}); err != nil {
				return fmt.Errorf("clear fact tables: %w", err)
			}
		}
		versionNo, err := q.NextVersionNo(ctx, sf.ID)
		if err != nil {
			return fmt.Errorf("next version number: %w", err)
		}
		in := queries.InsertFileVersionParams{
			SourceFileID: sf.ID, VersionNo: versionNo, SHA256: hash, SizeBytes: meta.Size, Etag: meta.ETag,
			LastModifiedAt: pgtype.Timestamptz{Time: meta.LastModified, Valid: true},
			State:          string(domain.FileVersionDiscovered),
		}
		oversize := meta.Size > s.MaxObjectBytes
		supported := Classify(key) != FormatUnsupported && !oversize
		if !supported {
			in.State = string(domain.FileVersionUnsupported)
			code := ParseErrorUnsupportedFormat
			if oversize {
				code = ParseErrorObjectTooLarge
				res.Oversize++
			}
			in.ParseErrorCode = &code
		}
		v, err := q.InsertFileVersion(ctx, in)
		if err != nil {
			return fmt.Errorf("insert version: %w", err)
		}
		res.NewVersions++
		if supported {
			created, err := jobs.New(q).Add(ctx, jobs.Enqueue{
				Kind: jobs.KindParse, Payload: map[string]string{"fileVersionId": v.ID.String()}, DedupeKey: v.ID.String(),
			})
			if err != nil {
				return err
			}
			if created {
				res.JobsEnqueued++
			}
		} else {
			res.Unsupported++
		}
		return q.UpdateObservedMetadata(ctx, observed)
	})
	if err != nil {
		return err
	}
	sum.NewVersions += res.NewVersions
	sum.MetadataOnly += res.MetadataOnly
	sum.Unsupported += res.Unsupported
	sum.Oversize += res.Oversize
	sum.JobsEnqueued += res.JobsEnqueued
	sum.StaleCandidates += res.StaleCandidates
	return nil
}

// removeObject moves the current version of a vanished key to REMOVED and its
// candidates to STALE.
func (s *Scanner) removeObject(ctx context.Context, r *stateRow, sum *Summary) error {
	var staled int64
	var removed bool
	err := pgx.BeginFunc(ctx, s.Store.Pool, func(tx pgx.Tx) error {
		q := s.Store.Queries.WithTx(tx)
		cur, err := q.GetCurrentFileVersion(ctx, r.SourceFileID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("load current version: %w", err)
		}
		if _, err := domain.FileVersionState(cur.State).Transition(domain.FileVersionRemoved); err != nil {
			return err
		}
		if err := mustAffect(q.RemoveFileVersion(ctx, cur.ID)); err != nil {
			return fmt.Errorf("remove version %s: %w", uuid.UUID(cur.ID), err)
		}
		if staled, err = q.MarkCandidatesStale(ctx, cur.ID); err != nil {
			return fmt.Errorf("stale candidates: %w", err)
		}
		if _, err := q.ClearFactTablesForVersion(ctx, queries.ClearFactTablesForVersionParams{VersionID: &cur.ID, ErrorCode: FactSourceRemoved}); err != nil {
			return fmt.Errorf("clear fact tables: %w", err)
		}
		removed = true
		return nil
	})
	if err != nil {
		return err
	}
	if removed {
		sum.Removed++
		sum.StaleCandidates += staled
	}
	return nil
}

func mustAffect(n int64, err error) error {
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("expected to change 1 row, changed %d", n)
	}
	return nil
}
