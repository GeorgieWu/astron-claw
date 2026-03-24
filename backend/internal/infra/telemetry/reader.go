package telemetry

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

func escapeLabel(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `"`, `\"`)
	v = strings.ReplaceAll(v, "\n", `\n`)
	return v
}

func promName(otelName string) string {
	return strings.ReplaceAll(otelName, ".", "_")
}

func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf(`%s="%s"`, k, escapeLabel(labels[k]))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func formatValue(v float64) string {
	if math.IsNaN(v) {
		return "NaN"
	}
	if math.IsInf(v, 1) {
		return "+Inf"
	}
	if math.IsInf(v, -1) {
		return "-Inf"
	}
	if v == float64(int64(v)) {
		return fmt.Sprintf("%d", int64(v))
	}
	return fmt.Sprintf("%v", v)
}

func parseField(field string) (string, map[string]string, bool) {
	idx := strings.Index(field, "|")
	if idx < 0 {
		return "", nil, false
	}
	name := field[:idx]
	var attrs map[string]string
	if err := json.Unmarshal([]byte(field[idx+1:]), &attrs); err != nil {
		return "", nil, false
	}
	return name, attrs, true
}

func injectService(attrs map[string]string, serviceName string) map[string]string {
	labels := make(map[string]string)
	if serviceName != "" {
		labels["service"] = serviceName
	}
	for k, v := range attrs {
		labels[k] = v
	}
	return labels
}

// RenderPrometheusExposition reads all metrics from Redis and renders Prometheus text.
func RenderPrometheusExposition(ctx context.Context, rdb redis.UniversalClient) (string, error) {
	var lines []string

	// Load resource info
	serviceName, err := rdb.HGet(ctx, KeyResource, "service.name").Result()
	if err != nil && err != redis.Nil {
		log.Warn().Err(err).Msg("telemetry: failed to read resource info")
	}

	// Load metadata
	metaRaw, err := rdb.HGetAll(ctx, KeyMeta).Result()
	if err != nil {
		log.Warn().Err(err).Msg("telemetry: failed to read metadata")
	}
	meta := make(map[string]map[string]string)
	for k, v := range metaRaw {
		var m map[string]string
		if json.Unmarshal([]byte(v), &m) == nil {
			meta[k] = m
		}
	}

	// Counters
	countersRaw, err := rdb.HGetAll(ctx, KeyCounters).Result()
	if err != nil {
		log.Warn().Err(err).Msg("telemetry: failed to read counters")
	}
	counterGroups := make(map[string][]struct {
		attrs map[string]string
		value float64
	})
	for field, val := range countersRaw {
		name, attrs, ok := parseField(field)
		if !ok {
			continue
		}
		v := parseFloat(val)
		counterGroups[name] = append(counterGroups[name], struct {
			attrs map[string]string
			value float64
		}{attrs, v})
	}

	sortedCounters := sortedKeys(counterGroups)
	for _, otelName := range sortedCounters {
		pname := promName(otelName) + "_total"
		m := meta[otelName]
		lines = append(lines, fmt.Sprintf("# HELP %s %s", pname, getOr(m, "description", "")))
		lines = append(lines, fmt.Sprintf("# TYPE %s counter", pname))
		for _, entry := range counterGroups[otelName] {
			labels := injectService(entry.attrs, serviceName)
			lines = append(lines, fmt.Sprintf("%s%s %s", pname, formatLabels(labels), formatValue(entry.value)))
		}
		lines = append(lines, "")
	}

	// Histograms
	histRaw, err := rdb.HGetAll(ctx, KeyHistograms).Result()
	if err != nil {
		log.Warn().Err(err).Msg("telemetry: failed to read histograms")
	}
	type histEntry struct {
		suffixes map[string]float64
	}
	histData := make(map[string]map[string]*histEntry) // name -> attrsJSON -> entry

	for field, val := range histRaw {
		parts := splitFromRight(field, "|")
		if len(parts) != 2 {
			continue
		}
		baseKey, suffix := parts[0], parts[1]
		name, attrs, ok := parseField(baseKey)
		if !ok {
			continue
		}
		attrsJSON, _ := json.Marshal(attrs)
		ak := string(attrsJSON)

		if histData[name] == nil {
			histData[name] = make(map[string]*histEntry)
		}
		if histData[name][ak] == nil {
			histData[name][ak] = &histEntry{suffixes: make(map[string]float64)}
		}
		histData[name][ak].suffixes[suffix] = parseFloat(val)
	}

	sortedHists := sortedKeys(histData)
	for _, otelName := range sortedHists {
		m := meta[otelName]
		pname := promName(otelName)
		if getOr(m, "unit", "") == "s" {
			pname += "_seconds"
		}
		lines = append(lines, fmt.Sprintf("# HELP %s %s", pname, getOr(m, "description", "")))
		lines = append(lines, fmt.Sprintf("# TYPE %s histogram", pname))

		attrsKeys := sortedKeys(histData[otelName])
		for _, ak := range attrsKeys {
			var attrs map[string]string
			json.Unmarshal([]byte(ak), &attrs)
			labels := injectService(attrs, serviceName)
			buckets := histData[otelName][ak].suffixes

			count := buckets["count"]
			totalSum := buckets["sum"]

			// Collect and sort bucket boundaries
			type bucketBound struct {
				bound float64
				count float64
			}
			var bounds []bucketBound
			for k, v := range buckets {
				if strings.HasPrefix(k, "bucket_") {
					boundStr := k[7:]
					if boundStr == "+Inf" {
						bounds = append(bounds, bucketBound{math.Inf(1), v})
					} else {
						bounds = append(bounds, bucketBound{parseFloat(boundStr), v})
					}
				}
			}
			sort.Slice(bounds, func(i, j int) bool { return bounds[i].bound < bounds[j].bound })

			// Convert delta to cumulative
			cumulative := 0.0
			for _, b := range bounds {
				cumulative += b.count
				leStr := "+Inf"
				if !math.IsInf(b.bound, 1) {
					leStr = formatValue(b.bound)
				}
				blabels := copyMap(labels)
				blabels["le"] = leStr
				lines = append(lines, fmt.Sprintf("%s_bucket%s %s", pname, formatLabels(blabels), formatValue(cumulative)))
			}
			lines = append(lines, fmt.Sprintf("%s_sum%s %s", pname, formatLabels(labels), formatValue(totalSum)))
			lines = append(lines, fmt.Sprintf("%s_count%s %s", pname, formatLabels(labels), formatValue(count)))
		}
		lines = append(lines, "")
	}

	// Gauges
	workerIDs, err := rdb.SMembers(ctx, KeyGaugePIDs).Result()
	if err != nil {
		log.Warn().Err(err).Msg("telemetry: failed to read gauge worker IDs")
	}
	gaugeAgg := make(map[string]float64)
	var deadWorkers []string

	for _, wid := range workerIDs {
		gk := GaugeKey(wid)
		fields, err := rdb.HGetAll(ctx, gk).Result()
		if err != nil {
			log.Warn().Err(err).Str("worker", wid).Msg("telemetry: failed to read gauge data")
		}
		if len(fields) == 0 {
			deadWorkers = append(deadWorkers, wid)
			continue
		}
		for field, val := range fields {
			gaugeAgg[field] += parseFloat(val)
		}
	}
	if len(deadWorkers) > 0 {
		rdb.SRem(ctx, KeyGaugePIDs, toInterfaces(deadWorkers)...)
	}

	gaugeGroups := make(map[string][]struct {
		attrs map[string]string
		value float64
	})
	for field, total := range gaugeAgg {
		name, attrs, ok := parseField(field)
		if !ok {
			continue
		}
		gaugeGroups[name] = append(gaugeGroups[name], struct {
			attrs map[string]string
			value float64
		}{attrs, total})
	}

	sortedGauges := sortedKeys(gaugeGroups)
	for _, otelName := range sortedGauges {
		pname := promName(otelName)
		m := meta[otelName]
		lines = append(lines, fmt.Sprintf("# HELP %s %s", pname, getOr(m, "description", "")))
		lines = append(lines, fmt.Sprintf("# TYPE %s gauge", pname))
		for _, entry := range gaugeGroups[otelName] {
			labels := injectService(entry.attrs, serviceName)
			lines = append(lines, fmt.Sprintf("%s%s %s", pname, formatLabels(labels), formatValue(entry.value)))
		}
		lines = append(lines, "")
	}

	return strings.Join(lines, "\n"), nil
}

// ResetAllMetrics deletes all OTLP metric keys from Redis.
func ResetAllMetrics(ctx context.Context, rdb redis.UniversalClient) error {
	keys := []string{KeyCounters, KeyHistograms, KeyMeta, KeyResource}
	wids, err := rdb.SMembers(ctx, KeyGaugePIDs).Result()
	if err != nil {
		log.Warn().Err(err).Msg("telemetry: failed to read gauge worker IDs for reset")
	}
	for _, wid := range wids {
		keys = append(keys, GaugeKey(wid))
	}
	keys = append(keys, KeyGaugePIDs)
	return rdb.Del(ctx, keys...).Err()
}

// Helper functions
func parseFloat(s string) float64 {
	v := 0.0
	fmt.Sscanf(s, "%f", &v)
	return v
}

func splitFromRight(s, sep string) []string {
	idx := strings.LastIndex(s, sep)
	if idx < 0 {
		return []string{s}
	}
	return []string{s[:idx], s[idx+1:]}
}

func getOr(m map[string]string, key, def string) string {
	if m == nil {
		return def
	}
	if v, ok := m[key]; ok {
		return v
	}
	return def
}

func copyMap(m map[string]string) map[string]string {
	c := make(map[string]string, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func toInterfaces(ss []string) []interface{} {
	out := make([]interface{}, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
