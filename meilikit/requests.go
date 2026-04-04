package meilikit

// searchRequest is the JSON body sent to POST /indexes/{uid}/search.
type searchRequest struct {
	Q string `json:"q"`
	SearchOptions
}

// buildSearchRequest converts a query string and SearchOptions into a searchRequest.
func buildSearchRequest(query string, opts SearchOptions) searchRequest {
	return searchRequest{Q: query, SearchOptions: opts}
}

// multiSearchRequest is the JSON body for POST /multi-search.
type multiSearchRequest struct {
	Queries []multiSearchQuery `json:"queries"`
}

// multiSearchQuery is a single query within a multi-search request body.
type multiSearchQuery struct {
	IndexUID string `json:"indexUid"`
	Q        string `json:"q"`
	SearchOptions
}

// buildMultiSearchRequest converts SearchQuery slices into the multi-search body.
func buildMultiSearchRequest(queries []SearchQuery) multiSearchRequest {
	mqs := make([]multiSearchQuery, len(queries))
	for i, q := range queries {
		mqs[i] = multiSearchQuery{
			IndexUID:      q.IndexUID,
			Q:             q.Query,
			SearchOptions: q.SearchOptions,
		}
	}
	return multiSearchRequest{Queries: mqs}
}

// settingsRequest is the JSON body for index settings updates.
type settingsRequest struct {
	SearchableAttributes []string            `json:"searchableAttributes,omitempty"`
	FilterableAttributes []string            `json:"filterableAttributes,omitempty"`
	SortableAttributes   []string            `json:"sortableAttributes,omitempty"`
	RankingRules         []string            `json:"rankingRules,omitempty"`
	StopWords            []string            `json:"stopWords,omitempty"`
	Synonyms             map[string][]string `json:"synonyms,omitempty"`
	Pagination           *PaginationConfig   `json:"pagination,omitempty"`
	TypoTolerance        *TypoConfig         `json:"typoTolerance,omitempty"`
}

// buildSettingsRequest converts an IndexConfig into a settings JSON body.
func buildSettingsRequest(cfg IndexConfig) settingsRequest {
	return settingsRequest{
		SearchableAttributes: cfg.Searchable,
		FilterableAttributes: cfg.Filterable,
		SortableAttributes:   cfg.Sortable,
		RankingRules:         cfg.RankingRules,
		StopWords:            cfg.StopWords,
		Synonyms:             cfg.Synonyms,
		Pagination:           cfg.Pagination,
		TypoTolerance:        cfg.TypoTolerance,
	}
}
