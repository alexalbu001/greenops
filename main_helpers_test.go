package main

import (
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// TestParseTags ensures that parseTags correctly converts a slice of EC2 tags into a map
func TestParseTags(t *testing.T) {
	tests := []struct {
		name  string
		input []types.Tag
		want  map[string]string
	}{
		{
			name:  "empty slice",
			input: []types.Tag{},
			want:  map[string]string{},
		},
		{
			name:  "single tag",
			input: []types.Tag{{Key: awsString("Env"), Value: awsString("prod")}},
			want:  map[string]string{"Env": "prod"},
		},
		{
			name: "multiple tags",
			input: []types.Tag{
				{Key: awsString("Name"), Value: awsString("web-server")},
				{Key: awsString("Owner"), Value: awsString("teamA")},
			},
			want: map[string]string{"Name": "web-server", "Owner": "teamA"},
		},
		{
			name: "nil keys or values ignored",
			input: []types.Tag{
				{Key: nil, Value: awsString("x")},
				{Key: awsString("Key1"), Value: nil},
				{Key: awsString("K2"), Value: awsString("V2")},
			},
			want: map[string]string{"K2": "V2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTags(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseTags() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Helper to create pointers to string values
func awsString(s string) *string {
	return &s
}
