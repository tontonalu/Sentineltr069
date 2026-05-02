package alerting

import (
	"testing"
	"time"
)

func TestParseWindow(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
		err  bool
	}{
		{"5m", 5 * time.Minute, false},
		{"1h", time.Hour, false},
		{"30s", 30 * time.Second, false},
		{"7d", 7 * 24 * time.Hour, false},
		{"", 0, true},
		{"abc", 0, true},
		{"-5m", 0, true},
		{"0s", 0, true},
	}
	for _, c := range cases {
		got, err := ParseWindow(c.in)
		if c.err {
			if err == nil {
				t.Errorf("input %q: expected error, got %v", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("input %q: unexpected error %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("input %q: got %v want %v", c.in, got, c.want)
		}
	}
}

func TestOperatorEval(t *testing.T) {
	cases := []struct {
		op   Operator
		v, t float64
		want bool
	}{
		{OpGT, 11, 10, true},
		{OpGT, 10, 10, false},
		{OpGTE, 10, 10, true},
		{OpLT, 5, 10, true},
		{OpLTE, 10, 10, true},
		{OpEQ, 10, 10, true},
		{OpNEQ, 11, 10, true},
	}
	for _, c := range cases {
		got := c.op.Eval(c.v, c.t)
		if got != c.want {
			t.Errorf("%v(%v, %v) = %v want %v", c.op, c.v, c.t, got, c.want)
		}
	}
}

func TestConditionValidate(t *testing.T) {
	t.Run("válida agregada", func(t *testing.T) {
		c := Condition{
			Type: TypeAggregate, Metric: MetricDeviceStatus,
			Aggregation: AggCountPct, Operator: OpGT, Threshold: 10, Window: "5m",
		}
		if err := c.Validate(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("metric desconhecida", func(t *testing.T) {
		c := Condition{Type: TypeAggregate, Metric: "x", Aggregation: AggCount, Operator: OpGT, Window: "5m"}
		if err := c.Validate(); err == nil {
			t.Fatal("esperava erro")
		}
	})
	t.Run("count_pct fora de device_status", func(t *testing.T) {
		c := Condition{Type: TypeAggregate, Metric: MetricCPUPct, Aggregation: AggCountPct, Operator: OpGT, Window: "5m"}
		if err := c.Validate(); err == nil {
			t.Fatal("esperava erro count_pct")
		}
	})
	t.Run("single sem device_id", func(t *testing.T) {
		c := Condition{Type: TypeSingle, Metric: MetricCPUPct, Aggregation: AggAvg, Operator: OpGT, Window: "5m"}
		if err := c.Validate(); err == nil {
			t.Fatal("esperava erro single")
		}
	})
}

func TestUnmarshalConditionRoundtrip(t *testing.T) {
	src := `{"type":"aggregate","metric":"device_status","filter":{"status":"offline"},"aggregation":"count_pct","operator":">","threshold":10,"window":"5m"}`
	c, err := UnmarshalCondition([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if c.Metric != MetricDeviceStatus || c.Threshold != 10 || c.Window != "5m" {
		t.Fatalf("parsed: %+v", c)
	}
	if c.Filter.Status != "offline" {
		t.Fatalf("filter status: %q", c.Filter.Status)
	}
	out, err := MarshalCondition(c)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := UnmarshalCondition(out)
	if err != nil {
		t.Fatal(err)
	}
	if c2.Threshold != c.Threshold || c2.Filter.Status != c.Filter.Status {
		t.Fatalf("roundtrip diverge: %+v vs %+v", c, c2)
	}
}
