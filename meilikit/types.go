package meilikit

import (
	"encoding/json"
	"fmt"
	"time"
)

// Config holds the Meilisearch connection settings, loaded via chassis config.
type Config struct {
	BaseURL string        `env:"MEILI_URL" default:"http://localhost:7700"`
	APIKey  string        `env:"MEILI_API_KEY" required:"false"`
	Timeout time.Duration `env:"MEILI_TIMEOUT" default:"5s"`
}

// IndexConfig defines index creation and settings.
type IndexConfig struct {
	PrimaryKey    string
	Searchable    []string
	Filterable    []string
	Sortable      []string
	RankingRules  []string
	StopWords     []string
	Synonyms      map[string][]string
	Pagination    *PaginationConfig
	TypoTolerance *TypoConfig
}

// PaginationConfig controls the maximum total hits returned.
type PaginationConfig struct {
	MaxTotalHits int `json:"maxTotalHits"`
}

// TypoConfig controls typo tolerance settings.
type TypoConfig struct {
	Enabled             *bool        `json:"enabled,omitempty"`
	MinWordSizeForTypos *MinWordSize `json:"minWordSizeForTypos,omitempty"`
}

// MinWordSize sets minimum word lengths for typo tolerance.
type MinWordSize struct {
	OneTypo  int `json:"oneTypo"`
	TwoTypos int `json:"twoTypos"`
}

// SearchOptions configures a search request.
type SearchOptions struct {
	Filter                string   `json:"filter,omitempty"`
	Facets                []string `json:"facets,omitempty"`
	Limit                 int64    `json:"limit,omitempty"`
	Offset                int64    `json:"offset,omitempty"`
	Sort                  []string `json:"sort,omitempty"`
	AttributesToRetrieve  []string `json:"attributesToRetrieve,omitempty"`
	AttributesToHighlight []string `json:"attributesToHighlight,omitempty"`
	AttributesToCrop      []string `json:"attributesToCrop,omitempty"`
	CropLength            int64    `json:"cropLength,omitempty"`
	HighlightPreTag       string   `json:"highlightPreTag,omitempty"`
	HighlightPostTag      string   `json:"highlightPostTag,omitempty"`
	MatchingStrategy      string   `json:"matchingStrategy,omitempty"`
	ShowMatchesPosition   bool     `json:"showMatchesPosition,omitempty"`
	ShowRankingScore      bool     `json:"showRankingScore,omitempty"`
}

// SearchResult is returned from a search or multi-search query.
type SearchResult struct {
	Hits               []json.RawMessage          `json:"hits"`
	Query              string                     `json:"query"`
	ProcessingTimeMs   int                        `json:"processingTimeMs"`
	EstimatedTotalHits *int                       `json:"estimatedTotalHits,omitempty"`
	TotalHits          *int                       `json:"totalHits,omitempty"`
	TotalPages         *int                       `json:"totalPages,omitempty"`
	Page               *int                       `json:"page,omitempty"`
	HitsPerPage        *int                       `json:"hitsPerPage,omitempty"`
	FacetDistribution  map[string]map[string]int  `json:"facetDistribution,omitempty"`
	FacetStats         map[string]FacetStat       `json:"facetStats,omitempty"`
}

// SearchHits unmarshals the raw hits from a SearchResult into a typed slice.
// Works with both single-search and multi-search results (via result.Results[i]).
func SearchHits[T any](result *SearchResult) ([]T, error) {
	out := make([]T, len(result.Hits))
	for i, raw := range result.Hits {
		if err := json.Unmarshal(raw, &out[i]); err != nil {
			return nil, fmt.Errorf("meilikit: unmarshal hit %d: %w", i, err)
		}
	}
	return out, nil
}

// FacetStat holds min/max values for a numeric facet.
type FacetStat struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

// BulkOptions configures a bulk import operation.
type BulkOptions struct {
	BatchSize  int
	OnProgress func(imported, total int)
}

// SearchQuery is a single query within a multi-search request.
type SearchQuery struct {
	IndexUID string `json:"indexUid"`
	Query    string `json:"q"`
	SearchOptions
}

// MultiSearchResult wraps the results of a multi-search call.
type MultiSearchResult struct {
	Results []SearchResult `json:"results"`
}

// TaskInfo is returned when Meilisearch enqueues an asynchronous task.
type TaskInfo struct {
	TaskUID    int64     `json:"taskUid"`
	IndexUID   string    `json:"indexUid"`
	Status     string    `json:"status"`
	Type       string    `json:"type"`
	EnqueuedAt time.Time `json:"enqueuedAt"`
}

// Task is the full representation of a Meilisearch task.
type Task struct {
	UID        int64      `json:"uid"`
	IndexUID   string     `json:"indexUid"`
	Status     string     `json:"status"`
	Type       string     `json:"type"`
	Error      *TaskError `json:"error,omitempty"`
	Duration   string     `json:"duration"`
	EnqueuedAt time.Time  `json:"enqueuedAt"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

// TaskError holds error details from a failed task.
type TaskError struct {
	Message string `json:"message"`
	Code    string `json:"code"`
	Type    string `json:"type"`
	Link    string `json:"link"`
}

// Settings represents Meilisearch index settings.
type Settings struct {
	SearchableAttributes []string            `json:"searchableAttributes,omitempty"`
	DisplayedAttributes  []string            `json:"displayedAttributes,omitempty"`
	FilterableAttributes []string            `json:"filterableAttributes,omitempty"`
	SortableAttributes   []string            `json:"sortableAttributes,omitempty"`
	RankingRules         []string            `json:"rankingRules,omitempty"`
	StopWords            []string            `json:"stopWords,omitempty"`
	Synonyms             map[string][]string `json:"synonyms,omitempty"`
	DistinctAttribute    *string             `json:"distinctAttribute,omitempty"`
	TypoTolerance        *TypoConfig         `json:"typoTolerance,omitempty"`
	Pagination           *PaginationConfig   `json:"pagination,omitempty"`
	Faceting             *FacetingConfig     `json:"faceting,omitempty"`
}

// FacetingConfig controls faceting behavior.
type FacetingConfig struct {
	MaxValuesPerFacet int `json:"maxValuesPerFacet"`
}
