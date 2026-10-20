package provider

import "testing"

// boolPtr is a helper for the tri-state PromptCache field.
func boolPtr(v bool) *bool { return &v }

func TestProvider_PRV23_CacheMarkerFromCatalog(t *testing.T) {
	RegisterModel(Model{ID: "prv23-implicit", Provider: "x", ImplicitCaching: true})
	RegisterModel(Model{ID: "prv23-explicit", Provider: "x"})
	cases := []struct {
		name string
		req  Request
		want bool
	}{
		{"unknown model", Request{Model: "prv23-missing"}, false},
		{"unknown model id falls back to the slug", Request{ModelID: "prv23-missing", Model: "prv23-explicit"}, true},
		{"implicit model", Request{ModelID: "prv23-implicit"}, false},
		{"non-implicit model", Request{ModelID: "prv23-explicit"}, true},
		{"catalog id wins over slug", Request{ModelID: "prv23-implicit", Model: "prv23-explicit"}, false},
	}
	for _, tc := range cases {
		got := RequestCacheControl(tc.req)
		if (got != nil) != tc.want {
			t.Errorf("%s: RequestCacheControl = %+v, want marker %v", tc.name, got, tc.want)
			continue
		}
		if got != nil && got.Type != "ephemeral" {
			t.Errorf("%s: marker type = %q, want ephemeral", tc.name, got.Type)
		}
	}
}

func TestProvider_PRV24_CacheMarkerOverride(t *testing.T) {
	RegisterModel(Model{ID: "prv24-implicit", Provider: "x", ImplicitCaching: true})
	RegisterModel(Model{ID: "prv24-explicit", Provider: "x"})
	cases := []struct {
		name string
		req  Request
		want bool
	}{
		{"opt in on an implicit model", Request{ModelID: "prv24-implicit", PromptCache: boolPtr(true)}, true},
		{"opt out on a non-implicit model", Request{ModelID: "prv24-explicit", PromptCache: boolPtr(false)}, false},
		{"opt out on an unknown model", Request{Model: "prv24-missing", PromptCache: boolPtr(false)}, false},
		{"opt in on an unknown model", Request{Model: "prv24-missing", PromptCache: boolPtr(true)}, true},
	}
	for _, tc := range cases {
		got := RequestCacheControl(tc.req)
		if (got != nil) != tc.want {
			t.Errorf("%s: RequestCacheControl = %+v, want marker %v", tc.name, got, tc.want)
		}
	}
}
