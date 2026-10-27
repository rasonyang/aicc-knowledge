// SPDX-License-Identifier: Apache-2.0

// Command resolve turns demo/novanet/questions.yaml into the CSV that
// `aicc-knowledge eval` reads, by looking up the candidate ids of the LIVE
// publications. It only reads PostgreSQL.
//
//	KB_DATABASE_URL=... KB_S3_PREFIX=novanet/ go run ./demo/novanet/resolve \
//	    -questions demo/novanet/questions.yaml -out eval.csv -index index.json -gaps gaps.txt
//
// A question whose expectations resolve to nothing is a generation or review
// gap: it is listed in the gaps file with the candidates that did come from
// the section, and it is left out of the CSV. The resolver never relabels it.
package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/jackc/pgx/v5"
)

type qRef struct {
	File    string `yaml:"file"`
	Section string `yaml:"section"`
	Fact    string `yaml:"fact,omitempty"`
}

type question struct {
	ID       string   `yaml:"id"`
	Language string   `yaml:"language"`
	Text     string   `yaml:"text"`
	Category string   `yaml:"category"`
	Tags     []string `yaml:"tags"`
	Live     bool     `yaml:"liveExample,omitempty"`
	Expect   []qRef   `yaml:"expect"`
}

type questionFile struct {
	Questions []question `yaml:"questions"`
}

// IndexEntry is one line of the index file, for joining eval results back to
// the question set.
type IndexEntry struct {
	ID          string   `json:"id"`
	Language    string   `json:"language"`
	Text        string   `json:"text"`
	Category    string   `json:"category"`
	Tags        []string `json:"tags"`
	Live        bool     `json:"liveExample,omitempty"`
	Status      string   `json:"status"` // RESOLVED, NO_MATCH or GAP
	ExpectedIDs []string `json:"expectedIds"`
	// GapKind says why a GAP is one: NO_CANDIDATE (nothing came from the
	// section), ONLY_REJECTED (all candidates of the section were rejected),
	// FACT_NOT_STATED (the section has published items, none states the fact).
	GapKind string `json:"gapKind,omitempty"`
	// SectionIDs are the published items of the section of a GAP whose fact
	// is not stated. They feed the optional lenient CSV and nothing else.
	SectionIDs []string `json:"sectionIds,omitempty"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "resolve:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("resolve", flag.ContinueOnError)
	qpath := fs.String("questions", "demo/novanet/questions.yaml", "question set")
	out := fs.String("out", "", "eval CSV to write (required)")
	index := fs.String("index", "", "JSON index of every question and its status")
	gaps := fs.String("gaps", "", "text report of the questions that resolve to nothing")
	lenient := fs.String("lenient-out", "", "optional second CSV: the GAP questions with FACT_NOT_STATED, expecting every published item of their section; it is a supplement and never replaces -out")
	if err := fs.Parse(args); err != nil {
		return err
	}
	dsn, prefix := os.Getenv("KB_DATABASE_URL"), os.Getenv("KB_S3_PREFIX")
	if dsn == "" || *out == "" {
		return fmt.Errorf("needs KB_DATABASE_URL and -out")
	}
	raw, err := os.ReadFile(*qpath)
	if err != nil {
		return err
	}
	var qf questionFile
	if err := yaml.Unmarshal(raw, &qf); err != nil {
		return fmt.Errorf("%s: %w", *qpath, err)
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, "SET default_transaction_read_only = on"); err != nil {
		return err
	}
	items, err := liveItems(ctx, conn)
	if err != nil {
		return err
	}
	cands, err := allCandidates(ctx, conn, prefix)
	if err != nil {
		return err
	}

	f, err := os.Create(*out)
	if err != nil {
		return err
	}
	defer f.Close()
	cw := csv.NewWriter(f)
	_ = cw.Write([]string{"question", "language", "expectedIds", "scope"})
	var lw *csv.Writer
	if *lenient != "" {
		lf, err := os.Create(*lenient)
		if err != nil {
			return err
		}
		defer lf.Close()
		lw = csv.NewWriter(lf)
		_ = lw.Write([]string{"question", "language", "expectedIds", "scope"})
	}
	var idx []IndexEntry
	var gapText strings.Builder
	nGap, nResolved, nNoMatch := 0, 0, 0
	for _, q := range qf.Questions {
		lang := strings.ToUpper(q.Language)
		e := IndexEntry{ID: q.ID, Language: lang, Text: q.Text, Category: q.Category, Tags: q.Tags, Live: q.Live}
		if len(q.Expect) == 0 {
			e.Status = "NO_MATCH"
			nNoMatch++
		} else {
			var exp []Expectation
			for _, r := range q.Expect {
				exp = append(exp, Expectation{File: r.File, Section: r.Section, Fact: r.Fact})
			}
			res := Resolve(prefix, exp, items[lang])
			e.ExpectedIDs = res.IDs
			if len(res.IDs) == 0 {
				e.Status = "GAP"
				nGap++
				for _, u := range exp {
					for _, it := range SectionItems(prefix, u, items[lang]) {
						e.SectionIDs = append(e.SectionIDs, it.CandidateID)
					}
				}
				e.GapKind = gapKind(prefix, exp, cands, len(e.SectionIDs) > 0)
				if lw != nil && e.GapKind == GapFactNotStated {
					_ = lw.Write([]string{q.Text, lang, strings.Join(e.SectionIDs, ";"), ""})
				}
				fmt.Fprintf(&gapText, "%s [%s] %s %s\n  %s\n", q.ID, q.Category, lang, e.GapKind, q.Text)
				for _, u := range res.Unresolved {
					fmt.Fprintf(&gapText, "  expects %s#%s fact=%q\n", u.File, u.Section, u.Fact)
					for _, c := range cands {
						if sourceMatches(prefix, u, c.SourceRef) {
							fmt.Fprintf(&gapText, "    came from the section: [%s] %s | %s\n", c.State, c.Question, c.Answer)
						}
					}
				}
				idx = append(idx, e)
				continue
			}
			e.Status = "RESOLVED"
			nResolved++
		}
		idx = append(idx, e)
		_ = cw.Write([]string{q.Text, lang, strings.Join(e.ExpectedIDs, ";"), ""})
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return err
	}
	if lw != nil {
		lw.Flush()
		if err := lw.Error(); err != nil {
			return err
		}
	}
	if *index != "" {
		b, _ := json.MarshalIndent(idx, "", "  ")
		if err := os.WriteFile(*index, b, 0o644); err != nil {
			return err
		}
	}
	if *gaps != "" {
		if err := os.WriteFile(*gaps, []byte(gapText.String()), 0o644); err != nil {
			return err
		}
	}
	fmt.Fprintf(stdout, "questions=%d resolved=%d no_match=%d gaps=%d\n", len(qf.Questions), nResolved, nNoMatch, nGap)
	return nil
}

// liveItems returns the published items of the LIVE publications by language.
func liveItems(ctx context.Context, conn *pgx.Conn) (map[string][]Item, error) {
	rows, err := conn.Query(ctx, `SELECT p.language, i.candidate_id::text, i.source_ref, i.question, i.answer
		FROM publications p JOIN publication_items i ON i.publication_id = p.id
		WHERE p.state = 'LIVE' ORDER BY i.source_ref, i.candidate_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]Item{}
	for rows.Next() {
		var lang string
		var it Item
		if err := rows.Scan(&lang, &it.CandidateID, &it.SourceRef, &it.Question, &it.Answer); err != nil {
			return nil, err
		}
		out[lang] = append(out[lang], it)
	}
	return out, rows.Err()
}

// allCandidates returns the candidates of any state, to explain a gap.
func allCandidates(ctx context.Context, conn *pgx.Conn, prefix string) ([]sectionCandidate, error) {
	rows, err := conn.Query(ctx, `SELECT id::text, state, question, answer, source_ref FROM candidates
		WHERE starts_with(source_ref, $1) ORDER BY source_ref, created_at`, prefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []sectionCandidate
	for rows.Next() {
		var c sectionCandidate
		if err := rows.Scan(&c.ID, &c.State, &c.Question, &c.Answer, &c.SourceRef); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
