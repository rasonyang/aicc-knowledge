// SPDX-License-Identifier: Apache-2.0

package obs

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Every metric name of the service lives in this block, so a dashboard author
// reads one place. Histograms are in seconds; the Prometheus names are the
// same strings.
const (
	MetricSearchEmbeddingSeconds = "kb_search_embedding_seconds"
	MetricSearchSearchSeconds    = "kb_search_search_seconds"
	MetricSearchTotalSeconds     = "kb_search_total_seconds"
	MetricHTTPRequestsTotal      = "kb_http_requests_total"
	MetricHTTPRequestSeconds     = "kb_http_request_seconds"
	MetricScanObjectsTotal       = "kb_scan_objects_total"
	MetricScanDownloadedBytes    = "kb_scan_downloaded_bytes_total"
	MetricScanSeconds            = "kb_scan_seconds"
	MetricParseJobsTotal         = "kb_parse_jobs_total"
	MetricParseSeconds           = "kb_parse_seconds"
	MetricFactsLookupsTotal      = "kb_facts_lookups_total"
	MetricGenerateCandidates     = "kb_generate_candidates_total"
	MetricGenerateSections       = "kb_generate_sections_total"
	MetricLLMRequestSeconds      = "kb_llm_request_seconds"
	MetricReviewRowsTotal        = "kb_review_rows_total"
	MetricPublishTotal           = "kb_publish_total"
	MetricPublishSeconds         = "kb_publish_seconds"
	MetricRollbackTotal          = "kb_rollback_total"
)

// Label keys (snake_case, Prometheus convention).
const (
	LabelLanguage = "language"
	LabelStatus   = "status"
	LabelMethod   = "method"
	LabelRoute    = "route"
	LabelCode     = "code"
	LabelOutcome  = "outcome"
	LabelKind     = "kind"
)

// latencyBuckets span a few milliseconds to the largest allowed timeout.
var latencyBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 60}

// Metrics are the instruments of the service.
type Metrics struct {
	searchEmbedding metric.Float64Histogram
	searchSearch    metric.Float64Histogram
	searchTotal     metric.Float64Histogram
	httpRequests    metric.Int64Counter
	httpSeconds     metric.Float64Histogram
	scanObjects     metric.Int64Counter
	scanBytes       metric.Int64Counter
	scanSeconds     metric.Float64Histogram
	parseJobs       metric.Int64Counter
	parseSeconds    metric.Float64Histogram
	factsLookups    metric.Int64Counter
	genCandidates   metric.Int64Counter
	genSections     metric.Int64Counter
	llmSeconds      metric.Float64Histogram
	reviewRows      metric.Int64Counter
	publishTotal    metric.Int64Counter
	publishSeconds  metric.Float64Histogram
	rollbackTotal   metric.Int64Counter
}

// NewMetrics registers every instrument on meter.
func NewMetrics(meter metric.Meter) (*Metrics, error) {
	hist := func(name, desc string) (metric.Float64Histogram, error) {
		return meter.Float64Histogram(name, metric.WithUnit("s"), metric.WithDescription(desc),
			metric.WithExplicitBucketBoundaries(latencyBuckets...))
	}
	var m Metrics
	var err error
	if m.searchEmbedding, err = hist(MetricSearchEmbeddingSeconds, "Time spent embedding the query in TEI."); err != nil {
		return nil, err
	}
	if m.searchSearch, err = hist(MetricSearchSearchSeconds, "Time spent searching Meilisearch."); err != nil {
		return nil, err
	}
	if m.searchTotal, err = hist(MetricSearchTotalSeconds, "Total time of a search request."); err != nil {
		return nil, err
	}
	if m.httpRequests, err = meter.Int64Counter(MetricHTTPRequestsTotal, metric.WithDescription("HTTP requests by method, route and status code.")); err != nil {
		return nil, err
	}
	if m.httpSeconds, err = hist(MetricHTTPRequestSeconds, "HTTP request duration."); err != nil {
		return nil, err
	}
	if m.scanObjects, err = meter.Int64Counter(MetricScanObjectsTotal, metric.WithDescription("Objects handled by the S3 scan, by outcome.")); err != nil {
		return nil, err
	}
	if m.scanBytes, err = meter.Int64Counter(MetricScanDownloadedBytes, metric.WithUnit("By"), metric.WithDescription("Bytes the S3 scan downloaded to hash.")); err != nil {
		return nil, err
	}
	if m.scanSeconds, err = meter.Float64Histogram(MetricScanSeconds, metric.WithUnit("s"), metric.WithDescription("Duration of one full S3 scan."),
		metric.WithExplicitBucketBoundaries(scanBuckets...)); err != nil {
		return nil, err
	}
	if m.parseJobs, err = meter.Int64Counter(MetricParseJobsTotal, metric.WithDescription("Parse jobs finished, by kind (DOCX, XLSX, FACTS_MAPPING) and outcome.")); err != nil {
		return nil, err
	}
	if m.parseSeconds, err = hist(MetricParseSeconds, "Duration of one parse job, by kind."); err != nil {
		return nil, err
	}
	if m.factsLookups, err = meter.Int64Counter(MetricFactsLookupsTotal, metric.WithDescription("Facts lookups by result status.")); err != nil {
		return nil, err
	}
	if m.genCandidates, err = meter.Int64Counter(MetricGenerateCandidates, metric.WithDescription("Candidates the LLM produced, by language and outcome (CREATED, DROPPED_INVALID, DROPPED_DUPLICATE).")); err != nil {
		return nil, err
	}
	if m.genSections, err = meter.Int64Counter(MetricGenerateSections, metric.WithDescription("Sections sent to the LLM, by outcome (OK, RETRIED, PARTIAL, EMPTY, ERROR).")); err != nil {
		return nil, err
	}
	if m.llmSeconds, err = meter.Float64Histogram(MetricLLMRequestSeconds, metric.WithUnit("s"), metric.WithDescription("Duration of one LLM completion including retries, by outcome (OK, ERROR code)."),
		metric.WithExplicitBucketBoundaries(llmBuckets...)); err != nil {
		return nil, err
	}
	if m.reviewRows, err = meter.Int64Counter(MetricReviewRowsTotal, metric.WithDescription("Rows of imported review workbooks, by outcome.")); err != nil {
		return nil, err
	}
	if m.publishTotal, err = meter.Int64Counter(MetricPublishTotal, metric.WithDescription("Publish runs by language and outcome (LIVE, FAILED, SKIPPED_EMPTY, or REFUSED_EMPTY).")); err != nil {
		return nil, err
	}
	if m.publishSeconds, err = meter.Float64Histogram(MetricPublishSeconds, metric.WithUnit("s"), metric.WithDescription("Duration of one publish run, by language and outcome."),
		metric.WithExplicitBucketBoundaries(scanBuckets...)); err != nil {
		return nil, err
	}
	if m.rollbackTotal, err = meter.Int64Counter(MetricRollbackTotal, metric.WithDescription("Rollbacks by language and outcome (LIVE, FAILED).")); err != nil {
		return nil, err
	}
	return &m, nil
}

// ObservePublish records one publish run. outcome is LIVE, FAILED,
// SKIPPED_EMPTY (nothing approved and nothing live) or REFUSED_EMPTY (would
// empty a live index without --allow-empty).
func (m *Metrics) ObservePublish(ctx context.Context, language, outcome string, d time.Duration) {
	attrs := metric.WithAttributes(attribute.String(LabelLanguage, language), attribute.String(LabelOutcome, outcome))
	m.publishTotal.Add(ctx, 1, attrs)
	m.publishSeconds.Record(ctx, d.Seconds(), attrs)
}

// ObserveRollback counts one rollback. outcome is LIVE or FAILED.
func (m *Metrics) ObserveRollback(ctx context.Context, language, outcome string) {
	m.rollbackTotal.Add(ctx, 1, metric.WithAttributes(attribute.String(LabelLanguage, language), attribute.String(LabelOutcome, outcome)))
}

// llmBuckets suit a completion that takes from a second to several minutes.
var llmBuckets = []float64{0.5, 1, 2, 5, 10, 20, 40, 60, 120, 300}

// ObserveGenerateCandidates adds n candidates to the counter of a language and
// outcome. Outcomes: CREATED, DROPPED_INVALID (a validation rule failed twice),
// DROPPED_DUPLICATE (the question already exists in the version).
func (m *Metrics) ObserveGenerateCandidates(ctx context.Context, language, outcome string, n int64) {
	m.genCandidates.Add(ctx, n, metric.WithAttributes(attribute.String(LabelLanguage, language), attribute.String(LabelOutcome, outcome)))
}

// ObserveGenerateSection counts one section handled by generate. Outcomes: OK,
// RETRIED (valid after a retry with feedback), PARTIAL (some candidates were
// dropped: a warning), EMPTY (no valid candidate), ERROR.
func (m *Metrics) ObserveGenerateSection(ctx context.Context, outcome string) {
	m.genSections.Add(ctx, 1, metric.WithAttributes(attribute.String(LabelOutcome, outcome)))
}

// ObserveLLMRequest records one completion. outcome is OK or an error code.
func (m *Metrics) ObserveLLMRequest(ctx context.Context, outcome string, d time.Duration) {
	m.llmSeconds.Record(ctx, d.Seconds(), metric.WithAttributes(attribute.String(LabelOutcome, outcome)))
}

// ObserveReviewRow counts one row of an imported review workbook. Outcomes:
// APPROVED, REJECTED, EDITED, SKIPPED, or the error code (STALE, NOT_PENDING,
// UNKNOWN_CANDIDATE, EDIT_REQUIRES_EDIT_ACTION, INVALID_EDIT, ...).
func (m *Metrics) ObserveReviewRow(ctx context.Context, outcome string) {
	m.reviewRows.Add(ctx, 1, metric.WithAttributes(attribute.String(LabelOutcome, outcome)))
}

// scanBuckets suit a scan that takes from a second to an hour.
var scanBuckets = []float64{0.1, 0.5, 1, 5, 15, 30, 60, 120, 300, 900, 1800, 3600}

// ObserveScanOutcome adds n objects to the outcome counter. Outcomes are
// SCREAMING_SNAKE: NEW_VERSION, UNCHANGED, METADATA_ONLY, REMOVED, UNSUPPORTED,
// IGNORED, OVERSIZE, ERROR. Zero counts are recorded too, so every series
// exists after the first scan.
func (m *Metrics) ObserveScanOutcome(ctx context.Context, outcome string, n int64) {
	m.scanObjects.Add(ctx, n, metric.WithAttributes(attribute.String(LabelOutcome, outcome)))
}

// ObserveScan records the bytes downloaded and the duration of one scan.
func (m *Metrics) ObserveScan(ctx context.Context, downloadedBytes int64, d time.Duration) {
	m.scanBytes.Add(ctx, downloadedBytes)
	m.scanSeconds.Record(ctx, d.Seconds())
}

// ObserveParse records one finished parse job. kind is DOCX, XLSX or
// FACTS_MAPPING; outcome is PARSED, PARSE_FAILED, SKIPPED or ERROR (an
// infrastructure failure that leaves the version DISCOVERED for a retry).
func (m *Metrics) ObserveParse(ctx context.Context, kind, outcome string, d time.Duration) {
	m.parseJobs.Add(ctx, 1, metric.WithAttributes(attribute.String(LabelKind, kind), attribute.String(LabelOutcome, outcome)))
	m.parseSeconds.Record(ctx, d.Seconds(), metric.WithAttributes(attribute.String(LabelKind, kind)))
}

// ObserveFactsLookup counts one facts lookup. status is FOUND, NOT_FOUND,
// TABLE_NOT_FOUND, TABLE_UNAVAILABLE or INVALID.
func (m *Metrics) ObserveFactsLookup(ctx context.Context, status string) {
	m.factsLookups.Add(ctx, 1, metric.WithAttributes(attribute.String(LabelStatus, status)))
}

// SearchStages are the per-stage durations of one search.
type SearchStages struct {
	Embedding time.Duration
	Search    time.Duration
	Total     time.Duration
}

// ObserveSearch records the stage histograms. status is HIT, NO_MATCH or an
// error code; language is EN or ZH.
func (m *Metrics) ObserveSearch(ctx context.Context, language, status string, d SearchStages) {
	attrs := metric.WithAttributes(attribute.String(LabelLanguage, language), attribute.String(LabelStatus, status))
	m.searchEmbedding.Record(ctx, d.Embedding.Seconds(), attrs)
	m.searchSearch.Record(ctx, d.Search.Seconds(), attrs)
	m.searchTotal.Record(ctx, d.Total.Seconds(), attrs)
}

// ObserveHTTP records one finished HTTP request.
func (m *Metrics) ObserveHTTP(ctx context.Context, method, route string, status int, d time.Duration) {
	m.httpRequests.Add(ctx, 1, metric.WithAttributes(
		attribute.String(LabelMethod, method), attribute.String(LabelRoute, route), attribute.Int(LabelCode, status)))
	m.httpSeconds.Record(ctx, d.Seconds(), metric.WithAttributes(
		attribute.String(LabelMethod, method), attribute.String(LabelRoute, route)))
}
