package model

import "time"

const Version = "0.1.0"
const PipelineVersion = "1"

type Profile struct {
	Widths        []int    `json:"widths"`
	Formats       []string `json:"formats"`
	IncludeSource bool     `json:"include_source"`
	JPEGQuality   int      `json:"jpeg_quality"`
	WebPQuality   int      `json:"webp_quality"`
	WebPEffort    int      `json:"webp_effort"`
	AVIFQuality   int      `json:"avif_quality"`
	AVIFEffort    int      `json:"avif_effort"`
}

type Limits struct {
	UploadBytes int64         `json:"upload_bytes"`
	Pixels      int64         `json:"pixels"`
	Dimension   int           `json:"dimension"`
	OutputCount int           `json:"output_count"`
	OutputBytes int64         `json:"output_bytes"`
	Duration    time.Duration `json:"duration_ns"`
}

type Input struct {
	Format        string `json:"format"`
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	Bytes         int64  `json:"bytes"`
	EncodedWidth  int    `json:"encoded_width"`
	EncodedHeight int    `json:"encoded_height"`
}

type Output struct {
	ID                    string  `json:"id"`
	Format                string  `json:"format"`
	Width                 int     `json:"width"`
	Height                int     `json:"height"`
	Bytes                 int64   `json:"bytes"`
	Path                  string  `json:"-"`
	URL                   string  `json:"url"`
	SizeDifferencePercent float64 `json:"size_difference_percent"`
	Smaller               bool    `json:"smaller"`
	IncludesResizing      bool    `json:"includes_resizing"`
}

type Job struct {
	ID           string  `json:"id"`
	ImageID      string  `json:"image_id"`
	Status       string  `json:"status"`
	Generated    int     `json:"generated"`
	Planned      int     `json:"planned"`
	ErrorCode    string  `json:"error_code,omitempty"`
	ErrorMessage string  `json:"error_message,omitempty"`
	DurationMS   int64   `json:"duration_ms"`
	Profile      Profile `json:"profile"`
	Pipeline     string  `json:"pipeline_version"`
	Token        string  `json:"-"`
	Attempt      int     `json:"attempt"`
}

type Image struct {
	ID          string    `json:"id"`
	JobID       string    `json:"job_id"`
	Status      string    `json:"status"`
	Original    Input     `json:"original"`
	OriginalURL string    `json:"original_url"`
	Outputs     []Output  `json:"outputs"`
	CreatedAt   time.Time `json:"created_at"`
}

type Task struct {
	InputPath string  `json:"input_path"`
	OutputDir string  `json:"output_dir"`
	Profile   Profile `json:"profile"`
	Limits    Limits  `json:"limits"`
}
