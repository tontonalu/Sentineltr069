package genieacs

import "testing"

func TestParamValueAndString(t *testing.T) {
	raw := map[string]any{
		"DeviceID": map[string]any{
			"_object": true,
			"Manufacturer": map[string]any{
				"_value":     "Huawei",
				"_type":      "xsd:string",
				"_timestamp": "2026-05-01T10:00:00Z",
			},
			"OUI": map[string]any{"_value": "00E0FC"},
		},
		"Device": map[string]any{
			"DeviceInfo": map[string]any{
				"UpTime":          map[string]any{"_value": float64(86400)},
				"X_Custom_Active": map[string]any{"_value": true},
			},
		},
	}

	t.Run("string scalar", func(t *testing.T) {
		if v := ParamString(raw, "DeviceID.Manufacturer"); v != "Huawei" {
			t.Errorf("got %q", v)
		}
	})

	t.Run("integer-as-float", func(t *testing.T) {
		if v := ParamString(raw, "Device.DeviceInfo.UpTime"); v != "86400" {
			t.Errorf("got %q", v)
		}
	})

	t.Run("boolean true", func(t *testing.T) {
		if v := ParamString(raw, "Device.DeviceInfo.X_Custom_Active"); v != "true" {
			t.Errorf("got %q", v)
		}
	})

	t.Run("missing path", func(t *testing.T) {
		if v := ParamString(raw, "DeviceID.Nonexistent"); v != "" {
			t.Errorf("got %q", v)
		}
	})

	t.Run("intermediate object", func(t *testing.T) {
		if v := ParamValue(raw, "DeviceID"); v != nil {
			t.Errorf("intermediário deveria ser nil, got %v", v)
		}
	})

	t.Run("first non empty", func(t *testing.T) {
		got := FirstNonEmpty(raw,
			"Missing.Path",
			"DeviceID.Manufacturer",
			"DeviceID.OUI")
		if got != "Huawei" {
			t.Errorf("got %q", got)
		}
	})
}
