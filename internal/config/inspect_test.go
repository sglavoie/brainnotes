package config

import "testing"

func TestEffectiveValueCoversSchema(t *testing.T) {
	cfg, err := DecodeSources(t.TempDir(), "", Sources{Portable: []byte("")})
	if err != nil {
		t.Fatal(err)
	}
	for _, layer := range []Layer{LayerPortable, LayerLocal} {
		for _, key := range LayerKeys(layer) {
			if _, err := cfg.EffectiveValue(key); err != nil {
				t.Errorf("%s: %v", key, err)
			}
		}
	}
	for _, key := range []string{"Vault", "Provenance", "serve", "views.missing", "bogus"} {
		if _, err := cfg.EffectiveValue(key); err == nil {
			t.Errorf("accepted non-schema key %s", key)
		}
	}
}
