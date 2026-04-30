package remote

import "testing"

func TestExtractBaseURLFromEndpointLog(t *testing.T) {
	got := extractBaseURLFromText(`2026-04-10 INFO Update endpoint success. endpoint config: https://ai-lingma-cmb01-cn-beijing.rdc.aliyuncs.com`)
	want := "https://ai-lingma-cmb01-cn-beijing.rdc.aliyuncs.com"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExtractBaseURLFromMarketplaceLog(t *testing.T) {
	got := extractBaseURLFromText(`2026-04-30 [info] [Marketplace] Using service url: https://ai-lingma-cmb01-cn-beijing.rdc.aliyuncs.com/marketplace/_apis/public/gallery`)
	want := "https://ai-lingma-cmb01-cn-beijing.rdc.aliyuncs.com"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
