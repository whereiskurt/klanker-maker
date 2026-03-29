package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// NewOtelCmd creates the "km otel" subcommand.
// Usage: km otel <sandbox-id>
//
// Shows a unified view of:
//   - Budget-enforcer AI spend from DynamoDB (per-model, with provider inference)
//   - OTEL telemetry data from S3 (log/metric/trace file counts)
//   - Latest OTEL metrics snapshot (Claude Code self-reported cost + tokens)
func NewOtelCmd(cfg *config.Config) *cobra.Command {
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
			return runOtel(ctx, cfg, sandboxID, cmd.OutOrStdout())
		},
	}
	return cmd
}

func runOtel(ctx context.Context, cfg *config.Config, sandboxID string, w io.Writer) error {
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

	fmt.Fprintf(w, "\nkm otel — %s\n", sandboxID)
	fmt.Fprintf(w, "────────────────────────────────────────────────────────────\n\n")

	// --- Section 1: Budget-enforcer AI spend (DynamoDB) ---
	budget, budgetErr := kmaws.GetBudget(ctx, dynClient, "km-budgets", sandboxID)
	if budgetErr != nil {
		fmt.Fprintf(w, "Budget: (error: %v)\n\n", budgetErr)
	} else {
		fmt.Fprintf(w, "Budget Enforcer (HTTP proxy → DynamoDB)\n")
		fmt.Fprintf(w, "  AI Spend: $%.4f / $%.4f (%.1f%%)\n",
			budget.AISpent, budget.AILimit, safePercent(budget.AISpent, budget.AILimit))

		if len(budget.AIByModel) > 0 {
			fmt.Fprintf(w, "\n")
			// Group by provider
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

	// --- Section 2: OTEL S3 data summary ---
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

	// --- Section 3: Latest OTEL metrics snapshot ---
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

type metricLine struct {
	name  string
	value string
}

func fetchLatestMetrics(ctx context.Context, client *s3.Client, bucket, prefix string) ([]metricLine, error) {
	// List to find the latest metrics file.
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

	// Find the latest by key (lexicographic — year/month/day/hour/minute path)
	var latestKey string
	for _, obj := range resp.Contents {
		if *obj.Key > latestKey {
			latestKey = *obj.Key
		}
	}

	// Fetch and parse.
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

// otlpMetricsPayload is the top-level OTLP JSON metrics structure.
type otlpMetricsPayload struct {
	ResourceMetrics []struct {
		ScopeMetrics []struct {
			Metrics []struct {
				Name string `json:"name"`
				Sum  *struct {
					DataPoints []struct {
						AsDouble   *float64               `json:"asDouble"`
						AsInt      *int64                  `json:"asInt"`
						Attributes []map[string]interface{} `json:"attributes"`
					} `json:"dataPoints"`
				} `json:"sum"`
				Gauge *struct {
					DataPoints []struct {
						AsDouble   *float64               `json:"asDouble"`
						AsInt      *int64                  `json:"asInt"`
						Attributes []map[string]interface{} `json:"attributes"`
					} `json:"dataPoints"`
				} `json:"gauge"`
			} `json:"metrics"`
		} `json:"scopeMetrics"`
	} `json:"resourceMetrics"`
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
				// Collect data points from sum or gauge.
				type dp struct {
					value float64
					attrs map[string]string
				}
				var points []dp

				extractPoints := func(dataPoints []struct {
					AsDouble   *float64               `json:"asDouble"`
					AsInt      *int64                  `json:"asInt"`
					Attributes []map[string]interface{} `json:"attributes"`
				}) {
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
					// Add key attributes to the label.
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

	// Sort by metric name for consistent output.
	sort.Slice(lines, func(i, j int) bool { return lines[i].name < lines[j].name })
	return lines, nil
}
