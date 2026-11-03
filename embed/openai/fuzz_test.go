package openai

import "testing"

// FuzzDecodeChunk proves an untrusted embeddings body cannot panic the parser or
// slip past the index checks; both outcomes are ordinary errors.
func FuzzDecodeChunk(f *testing.F) {
	f.Add([]byte(`{"data":[{"index":0,"embedding":[1,2]}],"usage":{"prompt_tokens":3,"cost":0.01}}`))
	f.Add([]byte(`{"data":[{"index":0,"embedding":[1,2]},{"index":0,"embedding":[3]}]}`))
	f.Add([]byte(`{"data":[{"index":-1,"embedding":[1]}]}`))
	f.Add([]byte(`{"data":[{"embedding":[1]}]}`))
	f.Add([]byte(`{"data":null}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _, _, _ = decodeChunk(data, 1)
	})
}
