package slack

import "testing"

// Wave 0 stubs for Plan 68-04 (UploadFile 3-step flow:
// files.getUploadURLExternal → POST upload → files.completeUploadExternal).
// Bodies will be replaced by the implementing plan.

func TestUploadFile_HappyPath(t *testing.T) {
	t.Skip("Wave 0 stub — Plan 68-04")
}

func TestUploadFile_Step1Failure(t *testing.T) {
	t.Skip("Wave 0 stub — Plan 68-04")
}

func TestUploadFile_Step2NetworkFailure(t *testing.T) {
	t.Skip("Wave 0 stub — Plan 68-04")
}

func TestUploadFile_Step2ChunkedRejected(t *testing.T) {
	t.Skip("Wave 0 stub — Plan 68-04")
}

func TestUploadFile_Step3Failure(t *testing.T) {
	t.Skip("Wave 0 stub — Plan 68-04")
}

func TestUploadFile_StreamingPassThrough(t *testing.T) {
	t.Skip("Wave 0 stub — Plan 68-04")
}

func TestUploadFile_OmitEmptyThreadTS(t *testing.T) {
	t.Skip("Wave 0 stub — Plan 68-04")
}
