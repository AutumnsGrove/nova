package openrouter

import "testing"

// FuzzDecodeResponse proves an untrusted answer body cannot panic the parser or
// slip past validation; both outcomes are ordinary errors.
func FuzzDecodeResponse(f *testing.F) {
	f.Add([]byte(validBody))
	f.Add([]byte(`{"answers":{"triage":{"choice":"z","confidence":2}}}`))
	f.Add([]byte(`{"answers":{"triage":{"type":"noul","noul":1.5}}}`))
	f.Add([]byte(`{"answers":{"triage":{"type":"score","score":9,"legend":{"0":"low"}}}}`))
	f.Add([]byte(`{"answers":null,"usage":{"input_tokens":-1}}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = decodeResponse(validRequest("vendor/model"), data)
	})
}

// FuzzDecodeModels does the same for the live model roster.
func FuzzDecodeModels(f *testing.F) {
	f.Add([]byte(`{"data":[{"id":"typesafe/jev-1.14","pricing":{"prompt":"0.0000001"}}]}`))
	f.Add([]byte(`{"data":null}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = decodeModels(data)
	})
}
