package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"sort"
	"time"

	"github.com/caarlos0/env/v9"
	"github.com/joho/godotenv"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/denisb0/sources_summary/models"
)

var (
	llmEnrichStartDate         = time.Date(2023, time.September, 29, 0, 0, 0, 0, time.UTC)
	llmEnrichEvalPeriod        = 30 * 24 * time.Hour
	stalledProcessingThreshold = 24 * time.Hour
)

type successRatio struct {
	startDate    time.Time
	endDate      time.Time
	successCount int
	totalCount   int
}

func newSuccessRatio(startDate time.Time, endDate time.Time) successRatio {
	return successRatio{
		startDate: startDate,
		endDate:   endDate,
	}
}

func (sr *successRatio) add(t time.Time, success bool) {
	if t.After(sr.startDate) && t.Before(sr.endDate) {
		sr.totalCount++
		if success {
			sr.successCount++
		}
	}
}

func (sr successRatio) value() float64 {
	if sr.totalCount == 0 {
		return 0
	}

	return float64(sr.successCount) / float64(sr.totalCount)
}

func latestDate(date time.Time, dates ...time.Time) time.Time {
	res := date

	for _, d := range dates {
		if d.After(res) {
			res = d
		}
	}

	return res
}

func evalActivity(entries []models.ContentEntry, now time.Time) float64 {
	minEntryCount := 20                        // just magic number
	maxInactiveInterval := 30 * 24 * time.Hour // another magic number. Sources which do not have posts for this period will be considered inactive
	entryCount := len(entries)

	if entryCount < minEntryCount {
		return 0 // not enough entries for evaluation
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].CreatedAt.Before(entries[j].CreatedAt) })

	firstCreated := entries[0].CreatedAt
	lastCreated := entries[entryCount-1].CreatedAt

	if now.Sub(lastCreated) > maxInactiveInterval {
		return 0 // not posting too long
	}

	if lastCreated.Sub(firstCreated) < 24*time.Hour {
		return 0 // all posts in one day, maybe only posted on day of addition
	}

	// average interval between posts over all time period
	avgInterval := lastCreated.Sub(firstCreated) / time.Duration(entryCount-1)

	// shorter period to compare, but at least one week
	verifyPeriod := time.Duration(math.Max(float64(avgInterval*time.Duration(minEntryCount)), float64(7*24*time.Hour)))
	verifyPeriodStart := now.Add(-verifyPeriod)
	var verifyEntryCount int
	for i, e := range entries {
		if e.CreatedAt.After(verifyPeriodStart) {
			verifyEntryCount = entryCount - i
			break
		}
	}

	if verifyEntryCount == 0 {
		fmt.Printf("source_id: %s, total entries: %v, avg_interval_h:%v, (last-first)d:%v, verifyPeriod_d:%v\n", entries[0].SourceID, entryCount, avgInterval.Hours(), lastCreated.Sub(firstCreated).Hours()/24, verifyPeriod.Hours()/24)
		return 0 // no posts in verify interval
	}
	// interval between posts in recent time
	verifyIntervalAvg := verifyPeriod / time.Duration(verifyEntryCount)

	// if result is < 1, posting is less frequent, > 1 more frequent
	return float64(avgInterval) / float64(verifyIntervalAvg)
}

func getEntryData(db *gorm.DB, sourceID string, summary *models.SummaryData, now time.Time) error {
	var entries []models.ContentEntry
	if err := db.Where(&models.ContentEntry{SourceID: sourceID}).Where("reject_reason <> ?", "low_engagement").Order(clause.OrderByColumn{Column: clause.Column{Name: "created_at"}, Desc: false}).Find(&entries).Error; err != nil {
		return err
	}

	summary.EntryCount = uint64(len(entries))

	if summary.EntryCount == 0 {
		return errors.New("no entries found")
	}

	lastActivity := entries[len(entries)-1].CreatedAt
	summary.DaysSinceLastEntry = now.Sub(lastActivity).Hours() / 24

	// entries participating in activity evaluation
	// ones that are completed, minus ones added on the day of first source scraping
	activityEntries := make([]models.ContentEntry, 0, len(entries))

	llmSuccess := newSuccessRatio(latestDate(llmEnrichStartDate, now.Add(-llmEnrichEvalPeriod)), now)

	for _, entry := range entries {
		if entry.Status == "completed" {
			summary.CompletedCount++

			if entry.EntryMetadata.Order == 0 {
				activityEntries = append(activityEntries, entry)
			}

			llmSuccess.add(entry.CreatedAt, entry.EntryMetadata.Enriched != nil)
		}

		if entry.RejectReason == "generic_error" {
			summary.ErrorCount++
		}
	}

	activeDuration := lastActivity.Sub(summary.AddedAt)

	if activeDuration > 0 {
		summary.AvgEntriesDay = math.Round(float64(len(activityEntries))/(activeDuration.Hours()/24)*100) / 100
	}

	if len(activityEntries) > 0 {
		summary.Activity = evalActivity(entries, now)
	}

	// check for llm processing efficiency
	summary.LLMEnrichedRatio = llmSuccess.value()

	return nil
}

func engagementThreshold(message json.RawMessage) (int, error) {

	engagementCfg := struct {
		Engagement struct {
			Threshold int `json:"threshold"`
		} `json:"engagement"`
	}{}

	if err := json.Unmarshal(message, &engagementCfg); err != nil {
		return 0, err
	}

	return engagementCfg.Engagement.Threshold, nil
}

func panicOnError(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	panicOnError(godotenv.Load())
	type Config struct {
		User     string `env:"DB_USER"`
		Password string `env:"DB_PASSWORD"`
		DBName   string `env:"DB_NAME"`
	}
	var cfg Config
	panicOnError(env.Parse(&cfg))

	dsn := fmt.Sprintf("host=localhost user=%s password=%s dbname=%s port=5432 sslmode=disable", cfg.User, cfg.Password, cfg.DBName)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatal("db access error", err)
	}

	var sourceIDs []string
	if err := db.Model(&models.ContentSource{}).Distinct().Pluck("source_id", &sourceIDs).Error; err != nil {
		log.Fatal("sources read error", err)
	}

	summaries := make([]models.SourceSummary, 0, len(sourceIDs))

	now := time.Now().UTC()

	sourcesLimit := 100

	for i, sid := range sourceIDs {
		if i == sourcesLimit {
			break
		}

		var sources []models.ContentSource
		var summary models.SummaryData

		err := db.Where(&models.ContentSource{SourceID: sid}).Find(&sources).Error
		if err != nil {
			fmt.Println("sources get error", err)
			goto ADDSUMMARY
		}

		summary.SourceCount = uint64(len(sources))
		summary.AddedAt = sources[0].CreatedAt

		for _, src := range sources {
			if src.Status == "processing" {
				summary.Enabled = true

				if now.Sub(src.UpdatedAt) > stalledProcessingThreshold {
					summary.StalledProcessing = true
				}
			}

			if src.CreatedAt.Before(summary.AddedAt) {
				summary.AddedAt = src.CreatedAt
			}

			engagement, _ := engagementThreshold(src.Options)
			if engagement > 0 {
				summary.EngagementCheck = true
			}

		}

		if summary.Enabled {
			if err := getEntryData(db, sid, &summary, now); err != nil {
				fmt.Printf("source %s entry data error %v\n", sid, err)
				goto ADDSUMMARY
			}
		}
	ADDSUMMARY:
		summaries = append(summaries, models.SourceSummary{
			SourceID:  sid,
			Summary:   summary,
			CreatedAt: now,
			UpdatedAt: now,
		})
	}

	// todo move to yggdrasil codebase and continue there
	fmt.Printf("summaries: %d\n", len(summaries))

	if !db.Migrator().HasTable(&models.SourceSummary{}) {
		panicOnError(db.Migrator().CreateTable(&models.SourceSummary{}))
	}

	for _, summary := range summaries {
		if err := db.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "source_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"summary", "updated_at"}),
		}).Create(&summary).Error; err != nil {
			fmt.Println(err)
		}
	}
}
