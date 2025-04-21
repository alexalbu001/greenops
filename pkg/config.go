package pkg

// Config holds the application configuration
type Config struct {
	API struct {
		URL     string `json:"url"`
		Timeout int    `json:"timeout"`
	} `json:"api"`

	AWS struct {
		Region  string `json:"region"`
		Profile string `json:"profile"`
	} `json:"aws"`

	Scan struct {
		Resources []string `json:"resources"`
		Limit     int      `json:"limit"`
		Metrics   struct {
			PeriodDays int `json:"period_days"`
		} `json:"metrics"`
	} `json:"scan"`

	Output struct {
		Colors    bool   `json:"colors"`
		Format    string `json:"format"`
		Verbosity string `json:"verbosity"`
	} `json:"output"`
}
