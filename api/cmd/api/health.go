package main

import (
	"bufio"
	"context"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/cache"
)

var startTime = time.Now()

const checkTimeout = 2 * time.Second

type healthEntry struct {
	ComponentName   string  `json:"componentName"`
	MeasurementName string  `json:"measurementName"`
	ComponentType   string  `json:"componentType"`
	ObservedValue   float64 `json:"observedValue"`
	ObservedUnit    string  `json:"observedUnit"`
	Status          string  `json:"status"`
	Time            string  `json:"time"`
}

type healthResponse struct {
	Status      string                 `json:"status"`
	Version     string                 `json:"version"`
	ReleaseID   string                 `json:"releaseId"`
	ServiceID   string                 `json:"serviceId"`
	Description string                 `json:"description"`
	Checks      map[string]healthEntry `json:"checks"`
}

func healthHandler(db *dynamodb.Client, pingTable string, valkeyClient *cache.Client, releaseID string, valkeyRequired bool) fiber.Handler {

	return func(c fiber.Ctx) error {
		now := time.Now().UTC().Format(time.RFC3339Nano)

		ctx, cancel := context.WithTimeout(c.Context(), checkTimeout)
		defer cancel()

		dynamo := checkDynamoDB(ctx, db, pingTable, now)
		valkey := checkValkey(ctx, valkeyClient, now, valkeyRequired)
		cpu := checkCPU(now)
		mem := checkMemory(now)
		uptime := healthEntry{
			ComponentName:   "server",
			MeasurementName: "uptime",
			ComponentType:   "system",
			ObservedValue:   time.Since(startTime).Seconds(),
			ObservedUnit:    "second",
			Status:          "pass",
			Time:            now,
		}

		checks := map[string]healthEntry{
			"dynamodb": dynamo,
			"valkey":   valkey,
			"cpu":      cpu,
			"memory":   mem,
			"uptime":   uptime,
		}

		overall := "pass"
		httpStatus := fiber.StatusOK
		for _, e := range []healthEntry{dynamo, valkey, cpu, mem} {
			if e.Status == "fail" {
				overall = "fail"
				httpStatus = fiber.StatusServiceUnavailable
				break
			}
		}
		if overall == "pass" {
			for _, e := range checks {
				if e.Status == "warn" {
					overall = "warn"
					httpStatus = 207
					break
				}
			}
		}

		c.Set(fiber.HeaderContentType, "application/health+json")
		return c.Status(httpStatus).JSON(healthResponse{
			Status:      overall,
			Version:     "1",
			ReleaseID:   releaseID,
			ServiceID:   "ctech-account",
			Description: "ctech-account Identity Provider",
			Checks:      checks,
		})
	}
}

func checkDynamoDB(ctx context.Context, db *dynamodb.Client, table, nowStr string) healthEntry {
	t0 := time.Now()
	_, err := db.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(table)})
	ms := roundOne(float64(time.Since(t0).Milliseconds()))
	st := "pass"
	if err != nil {
		st = "fail"
	}
	return healthEntry{"dynamodb", "responseTime", "datastore:database", ms, "ms", st, nowStr}
}

func checkValkey(ctx context.Context, cl *cache.Client, nowStr string, required bool) healthEntry {
	// Valkey is a hard dependency in production (OAuth codes, MFA tokens, rate
	// limiting all live there). A missing or unreachable cache is a functional
	// outage, not a degraded state — report it as "fail" so the LB drains the
	// instance (CAC-005). In dev (required=false) a disabled cache is tolerable,
	// so it stays "warn".
	if !cl.Enabled() {
		st := "warn"
		if required {
			st = "fail"
		}
		return healthEntry{"valkey", "responseTime", "datastore:cache", -1, "ms", st, nowStr}
	}
	t0 := time.Now()
	err := cl.Ping(ctx)
	ms := roundOne(float64(time.Since(t0).Milliseconds()))
	st := "pass"
	if err != nil {
		st = "fail"
	}
	return healthEntry{"valkey", "responseTime", "datastore:cache", ms, "ms", st, nowStr}
}

func checkCPU(nowStr string) healthEntry {
	pct := cpuPercent()
	st := "pass"
	if pct < 0 || pct > 90 {
		st = "warn"
	}
	return healthEntry{"cpu", "utilization", "system", pct, "percent", st, nowStr}
}

func checkMemory(nowStr string) healthEntry {
	pct := memoryPercent()
	st := "pass"
	if pct < 0 || pct > 90 {
		st = "warn"
	}
	return healthEntry{"memory", "utilization", "system", pct, "percent", st, nowStr}
}

func cpuSample() (idle, total int64, ok bool) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return 0, 0, false
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, false
	}
	var vals []int64
	for _, s := range fields[1:] {
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			break
		}
		vals = append(vals, v)
	}
	if len(vals) < 4 {
		return 0, 0, false
	}
	idle = vals[3]
	total = int64(0)
	for _, v := range vals {
		total += v
	}
	if total == 0 {
		return 0, 0, false
	}
	return idle, total, true
}

// cpuPercent reads /proc/stat twice ~150ms apart and divides the delta. A
// single sample (as before) is the since-boot average, which misleads
// autoscaling (BUG-040).
func cpuPercent() float64 {
	if runtime.GOOS != "linux" {
		return -1
	}
	idle1, total1, ok1 := cpuSample()
	if !ok1 {
		return -1
	}
	time.Sleep(150 * time.Millisecond)
	idle2, total2, ok2 := cpuSample()
	if !ok2 {
		return -1
	}
	idleDelta := idle2 - idle1
	totalDelta := total2 - total1
	if totalDelta <= 0 {
		return -1
	}
	return roundOne(100.0 * float64(totalDelta-idleDelta) / float64(totalDelta))
}

func memoryPercent() float64 {
	if runtime.GOOS != "linux" {
		return -1
	}
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return -1
	}
	defer f.Close()
	info := map[string]int64{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		valStr := strings.Fields(strings.TrimSpace(parts[1]))
		if len(valStr) == 0 {
			continue
		}
		v, err := strconv.ParseInt(valStr[0], 10, 64)
		if err == nil {
			info[key] = v
		}
	}
	total, ok1 := info["MemTotal"]
	available, ok2 := info["MemAvailable"]
	if !ok1 || !ok2 || total == 0 {
		return -1
	}
	return roundOne(100.0 * float64(total-available) / float64(total))
}

func roundOne(v float64) float64 {
	// Negative sentinels (e.g. -1 for "unavailable") must pass through unchanged;
	// the old rounding turned -1 into -0.9 (BUG-041).
	if v < 0 {
		return v
	}
	return float64(int64(v*10+0.5)) / 10
}
