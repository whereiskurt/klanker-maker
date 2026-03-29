package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

func NewOtelCmd(cfg *config.Config) *cobra.Command {
	var showPrompts bool
	var showEvents bool
	var showTimeline bool

	cmd := &cobra.Command{
		Use:   "otel <sandbox-id>",
		Short: "Show OTEL telemetry and AI spend summary for a sandbox",
		Long:  helpText("otel"),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			sandboxID, err := ResolveSandboxID(ctx, cfg, args[0])
			if err != nil {
				return err
			}
			opts := otelOpts{
				prompts:  showPrompts,
				events:   showEvents,
				timeline: showTimeline,
			}
			return runOtel(ctx, cfg, sandboxID, opts, cmd.OutOrStdout())
		},
	}
	cmd.Flags().BoolVar(&showPrompts, "prompts", false, "Show user prompts from OTEL logs")
	cmd.Flags().BoolVar(&showEvents, "events", false, "Show all OTEL log events (prompts, tool calls, API requests)")
	cmd.Flags().BoolVar(&showTimeline, "timeline", false, "Show conversation timeline with prompts, responses, and cost")
	return cmd
}

type otelOpts struct {
	prompts  bool
	events   bool
	timeline bool
}

func runOtel(ctx context.Context, cfg *config.Config, sandboxID string, opts otelOpts, w io.Writer) error {
	awsCfg, err := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}

	dynClient := dynamodb.NewFromConfig(awsCfg)
	s3Client := s3.NewFromConfig(awsCfg)

	bucket := cfg.ArtifactsBucket
	if bucket == "" {
		return fmt.Errorf("artifacts_bucket not configured — run km configure first")
	}

	// If a specific view was requested, show only that.
	if opts.prompts {
		return runOtelPrompts(ctx, s3Client, bucket, sandboxID, w)
	}
	if opts.events {
		return runOtelEvents(ctx, s3Client, bucket, sandboxID, w)
	}
	if opts.timeline {
		return runOtelTimeline(ctx, s3Client, bucket, sandboxID, w)
	}

	// Default: summary view.
	return runOtelSummary(ctx, dynClient, s3Client, bucket, sandboxID, w)
}

// ─── Summary view (default) ────────────────────────────────────────────────

func runOtelSummary(ctx context.Context, dynClient *dynamodb.Client, s3Client *s3.Client, bucket, sandboxID string, w io.Writer) error {
	fmt.Fprintf(w, "\nkm otel — %s\n", sandboxID)
	fmt.Fprintf(w, "────────────────────────────────────────────────────────────\n\n")

	// Budget-enforcer AI spend (DynamoDB).
	budget, budgetErr := kmaws.GetBudget(ctx, dynClient, "km-budgets", sandboxID)
	if budgetErr != nil {
		fmt.Fprintf(w, "Budget: (error: %v)\n\n", budgetErr)
	} else {
		fmt.Fprintf(w, "Budget Enforcer (HTTP proxy → DynamoDB)\n")
		fmt.Fprintf(w, "  AI Spend: $%.4f / $%.4f (%.1f%%)\n",
			budget.AISpent, budget.AILimit, safePercent(budget.AISpent, budget.AILimit))

		if len(budget.AIByModel) > 0 {
			fmt.Fprintf(w, "\n")
			var bedrockModels, directModels []string
			for model := range budget.AIByModel {
				if strings.HasPrefix(model, "anthropic.") {
					bedrockModels = append(bedrockModels, model)
				} else {
					directModels = append(directModels, model)
				}
			}
			sort.Strings(bedrockModels)
			sort.Strings(directModels)

			if len(bedrockModels) > 0 {
				fmt.Fprintf(w, "  Bedrock:\n")
				for _, model := range bedrockModels {
					ms := budget.AIByModel[model]
					fmt.Fprintf(w, "    %-45s $%.4f  (%dK in / %dK out)\n",
						model, ms.SpentUSD, ms.InputTokens/1000, ms.OutputTokens/1000)
				}
			}
			if len(directModels) > 0 {
				fmt.Fprintf(w, "  Anthropic Direct (Max/API):\n")
				for _, model := range directModels {
					ms := budget.AIByModel[model]
					fmt.Fprintf(w, "    %-45s $%.4f  (%dK in / %dK out)\n",
						model, ms.SpentUSD, ms.InputTokens/1000, ms.OutputTokens/1000)
				}
			}
		}
		fmt.Fprintf(w, "\n")
	}

	// OTEL S3 data summary.
	fmt.Fprintf(w, "OTEL Telemetry (OTel Collector → S3)\n")
	signals := []string{"logs", "metrics", "traces"}
	for _, signal := range signals {
		prefix := fmt.Sprintf("%s/%s/", signal, sandboxID)
		count, totalSize, latest, listErr := listS3Objects(ctx, s3Client, bucket, prefix)
		if listErr != nil {
			fmt.Fprintf(w, "  %-10s error: %v\n", signal+":", listErr)
			continue
		}
		if count == 0 {
			hint := ""
			if signal == "traces" {
				hint = " — Claude Code emits logs + metrics only"
			}
			fmt.Fprintf(w, "  %-10s (none%s)\n", signal+":", hint)
		} else {
			fmt.Fprintf(w, "  %-10s %d files, %s total, latest: %s\n",
				signal+":", count, humanSize(totalSize), latest)
		}
	}
	fmt.Fprintf(w, "  Bucket:    s3://%s/{signal}/%s/\n\n", bucket, sandboxID)

	// Latest OTEL metrics snapshot.
	fmt.Fprintf(w, "OTEL Metrics Snapshot (Claude Code self-reported)\n")
	metricsPrefix := fmt.Sprintf("metrics/%s/", sandboxID)
	latestMetrics, fetchErr := fetchLatestMetrics(ctx, s3Client, bucket, metricsPrefix)
	if fetchErr != nil {
		fmt.Fprintf(w, "  (no metrics data: %v)\n", fetchErr)
	} else {
		for _, m := range latestMetrics {
			fmt.Fprintf(w, "  %-45s %s\n", m.name, m.value)
		}
	}

	fmt.Fprintf(w, "\n")
	return nil
}

// ─── Prompts view (--prompts) ──────────────────────────────────────────────

func runOtelPrompts(ctx context.Context, s3Client *s3.Client, bucket, sandboxID string, w io.Writer) error {
	fmt.Fprintf(w, "\nkm otel --prompts — %s\n", sandboxID)
	fmt.Fprintf(w, "────────────────────────────────────────────────────────────\n\n")

	events, err := fetchAllLogEvents(ctx, s3Client, bucket, sandboxID)
	if err != nil {
		return fmt.Errorf("fetch logs: %w", err)
	}

	prompts := filterEvents(events, "user_prompt")
	if len(prompts) == 0 {
		fmt.Fprintf(w, "  (no prompts found)\n\n")
		return nil
	}

	for i, e := range prompts {
		ts := formatEventTime(e.Timestamp)
		prompt := e.Attrs["prompt"]
		sessionID := shortID(e.Attrs["session.id"])
		fmt.Fprintf(w, "  %d. [%s] session:%s\n", i+1, ts, sessionID)
		fmt.Fprintf(w, "     %s\n\n", prompt)
	}
	return nil
}

// ─── Events view (--events) ────────────────────────────────────────────────

func runOtelEvents(ctx context.Context, s3Client *s3.Client, bucket, sandboxID string, w io.Writer) error {
	fmt.Fprintf(w, "\nkm otel --events — %s\n", sandboxID)
	fmt.Fprintf(w, "────────────────────────────────────────────────────────────\n\n")

	events, err := fetchAllLogEvents(ctx, s3Client, bucket, sandboxID)
	if err != nil {
		return fmt.Errorf("fetch logs: %w", err)
	}

	if len(events) == 0 {
		fmt.Fprintf(w, "  (no events found)\n\n")
		return nil
	}

	for _, e := range events {
		ts := formatEventTime(e.Timestamp)
		switch e.EventName {
		case "user_prompt":
			prompt := e.Attrs["prompt"]
			if len(prompt) > 120 {
				prompt = prompt[:120] + "..."
			}
			fmt.Fprintf(w, "  [%s] 💬 prompt: %s\n", ts, prompt)
		case "api_request":
			model := e.Attrs["model"]
			cost := e.Attrs["cost_usd"]
			inTok := e.Attrs["input_tokens"]
			outTok := e.Attrs["output_tokens"]
			dur := e.Attrs["duration_ms"]
			cacheRead := e.Attrs["cache_read_tokens"]
			fmt.Fprintf(w, "  [%s] 🔄 api: %s  %sin/%sout  cache:%s  $%s  %sms\n",
				ts, model, inTok, outTok, cacheRead, cost, dur)
		case "tool_decision":
			fmt.Fprintf(w, "  [%s] 🔧 tool_decision\n", ts)
		case "tool_result":
			fmt.Fprintf(w, "  [%s] ✓  tool_result\n", ts)
		default:
			fmt.Fprintf(w, "  [%s] •  %s\n", ts, e.EventName)
		}
	}
	fmt.Fprintf(w, "\n  Total: %d events\n\n", len(events))
	return nil
}

// ─── Timeline view (--timeline) ────────────────────────────────────────────

func runOtelTimeline(ctx context.Context, s3Client *s3.Client, bucket, sandboxID string, w io.Writer) error {
	fmt.Fprintf(w, "\nkm otel --timeline — %s\n", sandboxID)
	fmt.Fprintf(w, "────────────────────────────────────────────────────────────\n\n")

	events, err := fetchAllLogEvents(ctx, s3Client, bucket, sandboxID)
	if err != nil {
		return fmt.Errorf("fetch logs: %w", err)
	}

	if len(events) == 0 {
		fmt.Fprintf(w, "  (no events found)\n\n")
		return nil
	}

	// Group events into conversation turns: each turn starts with a user_prompt
	// and includes all subsequent events until the next user_prompt.
	type turn struct {
		prompt    string
		timestamp string
		sessionID string
		apiCalls  []logEvent
		tools     int
		totalCost float64
	}

	var turns []turn
	var current *turn

	for _, e := range events {
		if e.EventName == "user_prompt" {
			if current != nil {
				turns = append(turns, *current)
			}
			current = &turn{
				prompt:    e.Attrs["prompt"],
				timestamp: formatEventTime(e.Timestamp),
				sessionID: shortID(e.Attrs["session.id"]),
			}
			continue
		}
		if current == nil {
			// Events before first prompt — create a synthetic turn.
			current = &turn{
				prompt:    "(session start)",
				timestamp: formatEventTime(e.Timestamp),
			}
		}
		switch e.EventName {
		case "api_request":
			current.apiCalls = append(current.apiCalls, e)
			if cost, ok := e.Attrs["cost_usd"]; ok {
				var c float64
				fmt.Sscanf(cost, "%f", &c)
				current.totalCost += c
			}
		case "tool_decision", "tool_result":
			current.tools++
		}
	}
	if current != nil {
		turns = append(turns, *current)
	}

	var grandTotal float64
	for i, t := range turns {
		grandTotal += t.totalCost
		prompt := t.prompt
		if len(prompt) > 100 {
			prompt = prompt[:100] + "..."
		}

		fmt.Fprintf(w, "── Turn %d  [%s]  session:%s ──\n", i+1, t.timestamp, t.sessionID)
		fmt.Fprintf(w, "  User: %s\n", prompt)

		if len(t.apiCalls) == 0 {
			fmt.Fprintf(w, "  (no API calls)\n")
		} else {
			for _, api := range t.apiCalls {
				model := api.Attrs["model"]
				inTok := api.Attrs["input_tokens"]
				outTok := api.Attrs["output_tokens"]
				cacheRead := api.Attrs["cache_read_tokens"]
				cacheCreate := api.Attrs["cache_creation_tokens"]
				cost := api.Attrs["cost_usd"]
				dur := api.Attrs["duration_ms"]

				cacheStr := ""
				if cacheRead != "0" || cacheCreate != "0" {
					cacheStr = fmt.Sprintf("  cache: %s read, %s created", cacheRead, cacheCreate)
				}
				fmt.Fprintf(w, "  → %s  %s in / %s out  $%s  %sms%s\n",
					model, inTok, outTok, cost, dur, cacheStr)
			}
		}

		if t.tools > 0 {
			fmt.Fprintf(w, "  Tools: %d calls\n", t.tools/2) // decision + result = 1 tool use
		}
		fmt.Fprintf(w, "  Turn cost: $%.6f\n\n", t.totalCost)
	}

	fmt.Fprintf(w, "────────────────────────────────────────────────────────────\n")
	fmt.Fprintf(w, "  Turns: %d  |  Total OTEL cost: $%.6f\n\n", len(turns), grandTotal)
	return nil
}

// ─── Shared: OTLP log event parsing ────────────────────────────────────────

type logEvent struct {
	EventName string
	Timestamp string // ISO 8601
	Sequence  int
	Attrs     map[string]string
}

func fetchAllLogEvents(ctx context.Context, client *s3.Client, bucket, sandboxID string) ([]logEvent, error) {
	prefix := fmt.Sprintf("logs/%s/", sandboxID)
	paginator := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{
		Bucket: awssdk.String(bucket),
		Prefix: awssdk.String(prefix),
	})

	var keys []string
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, obj := range page.Contents {
			keys = append(keys, *obj.Key)
		}
	}
	sort.Strings(keys)

	var allEvents []logEvent
	for _, key := range keys {
		resp, err := client.GetObject(ctx, &s3.GetObjectInput{
			Bucket: awssdk.String(bucket),
			Key:    awssdk.String(key),
		})
		if err != nil {
			continue
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			continue
		}

		events := parseOTLPLogs(data)
		allEvents = append(allEvents, events...)
	}

	// Sort by timestamp then sequence.
	sort.Slice(allEvents, func(i, j int) bool {
		if allEvents[i].Timestamp != allEvents[j].Timestamp {
			return allEvents[i].Timestamp < allEvents[j].Timestamp
		}
		return allEvents[i].Sequence < allEvents[j].Sequence
	})

	return allEvents, nil
}

type otlpLogsPayload struct {
	ResourceLogs []struct {
		ScopeLogs []struct {
			LogRecords []struct {
				Body       map[string]interface{} `json:"body"`
				Attributes []struct {
					Key   string                 `json:"key"`
					Value map[string]interface{} `json:"value"`
				} `json:"attributes"`
				TimeUnixNano string `json:"timeUnixNano"`
			} `json:"logRecords"`
		} `json:"scopeLogs"`
	} `json:"resourceLogs"`
}

func parseOTLPLogs(data []byte) []logEvent {
	var payload otlpLogsPayload
	if json.Unmarshal(data, &payload) != nil {
		return nil
	}

	var events []logEvent
	for _, rl := range payload.ResourceLogs {
		for _, sl := range rl.ScopeLogs {
			for _, lr := range sl.LogRecords {
				attrs := make(map[string]string)
				for _, a := range lr.Attributes {
					for _, v := range a.Value {
						attrs[a.Key] = fmt.Sprintf("%v", v)
					}
				}

				seq := 0
				if s, ok := attrs["event.sequence"]; ok {
					fmt.Sscanf(s, "%d", &seq)
				}

				ts := attrs["event.timestamp"]
				if ts == "" && lr.TimeUnixNano != "" {
					// Parse nanosecond epoch.
					var nanos int64
					fmt.Sscanf(lr.TimeUnixNano, "%d", &nanos)
					if nanos > 0 {
						ts = time.Unix(0, nanos).UTC().Format(time.RFC3339)
					}
				}

				events = append(events, logEvent{
					EventName: attrs["event.name"],
					Timestamp: ts,
					Sequence:  seq,
					Attrs:     attrs,
				})
			}
		}
	}
	return events
}

func filterEvents(events []logEvent, eventName string) []logEvent {
	var filtered []logEvent
	for _, e := range events {
		if e.EventName == eventName {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

func formatEventTime(ts string) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		t, err = time.Parse("2006-01-02T15:04:05.000Z", ts)
		if err != nil {
			return ts
		}
	}
	return t.Local().Format("15:04:05")
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// ─── Shared: S3 helpers ────────────────────────────────────────────────────

func safePercent(spent, limit float64) float64 {
	if limit <= 0 {
		return 0
	}
	return (spent / limit) * 100
}

func humanSize(bytes int64) string {
	switch {
	case bytes >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
	case bytes >= 1024:
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

func listS3Objects(ctx context.Context, client *s3.Client, bucket, prefix string) (count int, totalSize int64, latest string, err error) {
	paginator := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{
		Bucket: awssdk.String(bucket),
		Prefix: awssdk.String(prefix),
	})

	var latestTime string
	for paginator.HasMorePages() {
		page, pageErr := paginator.NextPage(ctx)
		if pageErr != nil {
			return 0, 0, "", pageErr
		}
		for _, obj := range page.Contents {
			count++
			totalSize += *obj.Size
			ts := obj.LastModified.Format("2006-01-02 15:04:05")
			if ts > latestTime {
				latestTime = ts
			}
		}
	}
	return count, totalSize, latestTime, nil
}

// ─── Shared: OTLP metrics parsing ─────────────────────────────────────────

type metricLine struct {
	name  string
	value string
}

func fetchLatestMetrics(ctx context.Context, client *s3.Client, bucket, prefix string) ([]metricLine, error) {
	resp, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: awssdk.String(bucket),
		Prefix: awssdk.String(prefix),
	})
	if err != nil {
		return nil, err
	}
	if len(resp.Contents) == 0 {
		return nil, fmt.Errorf("no files")
	}

	var latestKey string
	for _, obj := range resp.Contents {
		if *obj.Key > latestKey {
			latestKey = *obj.Key
		}
	}

	getResp, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: awssdk.String(bucket),
		Key:    awssdk.String(latestKey),
	})
	if err != nil {
		return nil, err
	}
	defer getResp.Body.Close()

	data, err := io.ReadAll(getResp.Body)
	if err != nil {
		return nil, err
	}

	return parseOTLPMetrics(data)
}

type otlpMetricsPayload struct {
	ResourceMetrics []struct {
		ScopeMetrics []struct {
			Metrics []struct {
				Name string `json:"name"`
				Sum  *struct {
					DataPoints []otlpDataPoint `json:"dataPoints"`
				} `json:"sum"`
				Gauge *struct {
					DataPoints []otlpDataPoint `json:"dataPoints"`
				} `json:"gauge"`
			} `json:"metrics"`
		} `json:"scopeMetrics"`
	} `json:"resourceMetrics"`
}

type otlpDataPoint struct {
	AsDouble   *float64               `json:"asDouble"`
	AsInt      *int64                  `json:"asInt"`
	Attributes []map[string]interface{} `json:"attributes"`
}

func parseOTLPMetrics(data []byte) ([]metricLine, error) {
	var payload otlpMetricsPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("parse OTLP JSON: %w", err)
	}

	var lines []metricLine
	for _, rm := range payload.ResourceMetrics {
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				type dp struct {
					value float64
					attrs map[string]string
				}
				var points []dp

				extractPoints := func(dataPoints []otlpDataPoint) {
					for _, d := range dataPoints {
						var val float64
						if d.AsDouble != nil {
							val = *d.AsDouble
						} else if d.AsInt != nil {
							val = float64(*d.AsInt)
						}
						attrs := make(map[string]string)
						for _, a := range d.Attributes {
							if k, ok := a["key"].(string); ok {
								if v, ok := a["value"].(map[string]interface{}); ok {
									for _, sv := range v {
										attrs[k] = fmt.Sprintf("%v", sv)
									}
								}
							}
						}
						points = append(points, dp{value: val, attrs: attrs})
					}
				}

				if m.Sum != nil {
					extractPoints(m.Sum.DataPoints)
				}
				if m.Gauge != nil {
					extractPoints(m.Gauge.DataPoints)
				}

				for _, p := range points {
					label := m.Name
					if model, ok := p.attrs["model"]; ok {
						label += " [" + model + "]"
					}
					if typ, ok := p.attrs["type"]; ok {
						label += " (" + typ + ")"
					}

					var valStr string
					if strings.Contains(m.Name, "cost") {
						valStr = fmt.Sprintf("$%.6f", p.value)
					} else if strings.Contains(m.Name, "time") {
						valStr = fmt.Sprintf("%.1fs", p.value)
					} else if p.value == float64(int64(p.value)) {
						valStr = fmt.Sprintf("%d", int64(p.value))
					} else {
						valStr = fmt.Sprintf("%.4f", p.value)
					}
					lines = append(lines, metricLine{name: label, value: valStr})
				}
			}
		}
	}

	sort.Slice(lines, func(i, j int) bool { return lines[i].name < lines[j].name })
	return lines, nil
}
