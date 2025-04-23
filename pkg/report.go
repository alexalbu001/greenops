package pkg

import (
	"encoding/json"
)

// ResourceType represents the type of AWS resource
type ResourceType string

const (
	ResourceTypeEC2 ResourceType = "ec2"
	ResourceTypeS3  ResourceType = "s3"
	ResourceTypeRDS ResourceType = "rds"
	ResourceTypeEBS ResourceType = "ebs"
)

// ReportItem represents a single analyzed resource
type ReportItem struct {
	ResourceType ResourceType `json:"resource_type,omitempty"`
	Instance     Instance     `json:"instance,omitempty"`
	S3Bucket     S3Bucket     `json:"s3_bucket,omitempty"`
	RDSInstance  RDSInstance  `json:"rds_instance,omitempty"`
	Embedding    []float64    `json:"embedding,omitempty"`
	Analysis     string       `json:"analysis"`
}

// GetResourceType explicitly determines the type of resource based on data
func (r *ReportItem) GetResourceType() ResourceType {
	// If ResourceType is already set, use it
	if r.ResourceType != "" {
		return r.ResourceType
	}

	// Otherwise determine type based on which fields are populated
	if !IsEmptyObject(r.Instance) && r.Instance.InstanceID != "" {
		return ResourceTypeEC2
	}

	if !IsEmptyObject(r.S3Bucket) && r.S3Bucket.BucketName != "" {
		return ResourceTypeS3
	}

	if !IsEmptyObject(r.RDSInstance) && r.RDSInstance.InstanceID != "" {
		return ResourceTypeRDS
	}

	// Default to EC2 for backward compatibility
	return ResourceTypeEC2
}

// Custom JSON marshalling to ensure proper type handling
func (r ReportItem) MarshalJSON() ([]byte, error) {
	// Set resource type if not already set
	if r.ResourceType == "" {
		r.ResourceType = r.GetResourceType()
	}

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
		// Set resource type if not already set
		if r.ResourceType == "" {
			r.ResourceType = r.GetResourceType()
		}
		return nil
	}

	// If standard unmarshalling fails, try handling it as a JSON string
	var jsonString string
	if err := json.Unmarshal(data, &jsonString); err == nil {
		// If it's a string, try to unmarshal the string content
		err = json.Unmarshal([]byte(jsonString), r)
		// Set resource type if unmarshalling succeeded
		if err == nil && r.ResourceType == "" {
			r.ResourceType = r.GetResourceType()
		}
		return err
	}

	// Return the original error if all attempts fail
	return err
}
