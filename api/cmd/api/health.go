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

func healthHandler(db *dynamodb.Client, pingTable string, valkeyClient *cache.Client, releaseID string) fiber.Handler {

	return func(c fiber.Ctx) error {
		now := time.Now().UTC().Format(time.RFC3339Nano)

		ctx, cancel := context.WithTimeout(c.Context(), checkTimeout)
		defer cancel()

		dynamo := checkDynamoDB(ctx, db, pingTable, now)
		valkey := checkValkey(ctx, valkeyClient, now)
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
		for _, e := range []healthEntry{dynamo, cpu, mem} {
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

func checkValkey(ctx context.Context, cl *cache.Client, nowStr string) healthEntry {
	if !cl.Enabled() {
		return healthEntry{"valkey", "responseTime", "datastore:cache", -1, "ms", "warn", nowStr}
	}
	t0 := time.Now()
	err := cl.Ping(ctx)
	ms := roundOne(float64(time.Since(t0).Milliseconds()))
	st := "pass"
	if err != nil {
		st = "warn"
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

func cpuPercent() float64 {
	if runtime.GOOS != "linux" {
		return -1
	}
	f, err := os.Open("/proc/stat")
	if err != nil {
		return -1
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return -1
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return -1
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
		return -1
	}
	idle := vals[3]
	total := int64(0)
	for _, v := range vals {
		total += v
	}
	if total == 0 {
		return -1
	}
	return roundOne(100.0 * float64(total-idle) / float64(total))
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
	return float64(int64(v*10+0.5)) / 10
}
