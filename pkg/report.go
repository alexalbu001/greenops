package pkg

import (
	"encoding/json"
)

// ReportItem represents a single analyzed resource
type ReportItem struct {
	Instance  Instance  `json:"instance,omitempty"`
	S3Bucket  S3Bucket  `json:"s3_bucket,omitempty"`
	Embedding []float64 `json:"embedding,omitempty"`
	Analysis  string    `json:"analysis"`
}

// Custom JSON marshalling to ensure proper type handling
func (r ReportItem) MarshalJSON() ([]byte, error) {
	type Alias ReportItem
	return json.Marshal(&struct {
		Alias
	}{
		Alias: Alias(r),
	})
}

// Custom JSON unmarshalling to handle string representations
func (r *ReportItem) UnmarshalJSON(data []byte) error {
	// Try standard unmarshalling first
	type Alias ReportItem
	aux := &struct {
		Alias
	}{}

	err := json.Unmarshal(data, aux)
	if err == nil {
		*r = ReportItem(aux.Alias)
		return nil
	}

	// If standard unmarshalling fails, try handling it as a JSON string
	var jsonString string
	if err := json.Unmarshal(data, &jsonString); err == nil {
		// If it's a string, try to unmarshal the string content
		return json.Unmarshal([]byte(jsonString), r)
	}

	// Return the original error if all attempts fail
	return err
}
