package pkg

// ReportItem represents a single analyzed instance
type ReportItem struct {
	Instance  Instance  `json:"instance"`
	Embedding []float64 `json:"embedding"`
	Analysis  string    `json:"analysis"`
}
