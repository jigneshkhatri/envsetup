package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jigneshkhatri/envsetup/internal/core"
)

func TestPrintScanEmpty(t *testing.T) {
	var buf bytes.Buffer
	PrintScan(&buf, map[string][]core.Resource{}, false)
	if !strings.Contains(buf.String(), "nothing to scan") {
		t.Errorf("got %q", buf.String())
	}
}

func TestPrintScanListsResources(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	found := map[string][]core.Resource{
		"widget": {
			{Type: "widget", ID: "widget-a", Confidence: core.ConfidenceHigh, Provenance: core.Provenance{Source: "test"}},
		},
	}

	var buf bytes.Buffer
	PrintScan(&buf, found, false)

	out := buf.String()
	if !strings.Contains(out, "widget-a") || !strings.Contains(out, "high") || !strings.Contains(out, "test") {
		t.Errorf("scan output missing expected fields: %s", out)
	}
}

func TestPrintScanVerboseShowsAttributes(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	found := map[string][]core.Resource{
		"widget": {
			{Type: "widget", ID: "widget-a", Confidence: core.ConfidenceHigh, Attributes: map[string]any{"value": "v1"}},
		},
	}

	var buf bytes.Buffer
	PrintScan(&buf, found, true)

	if !strings.Contains(buf.String(), "widget-a.value: v1") {
		t.Errorf("verbose scan output missing attribute detail: %s", buf.String())
	}
}

func TestPrintPlanNoChanges(t *testing.T) {
	var buf bytes.Buffer
	PrintPlan(&buf, nil)
	if !strings.Contains(buf.String(), "No changes") {
		t.Errorf("got %q", buf.String())
	}
}

func TestPrintPlanSummary(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	actions := []core.Action{
		{ResourceType: "widget", ResourceID: "a", Kind: core.ActionCreate, Description: "create a"},
		{ResourceType: "widget", ResourceID: "b", Kind: core.ActionUpdate, Description: "update b"},
		{ResourceType: "widget", ResourceID: "c", Kind: core.ActionDelete, Description: "delete c"},
	}

	var buf bytes.Buffer
	PrintPlan(&buf, actions)

	out := buf.String()
	if !strings.Contains(out, "1 to create, 1 to update, 1 to delete") {
		t.Errorf("got %q", out)
	}
}

func TestPrintValidationNoDrift(t *testing.T) {
	var buf bytes.Buffer
	PrintValidation(&buf, []core.ValidationResult{{ResourceType: "widget", ResourceID: "a", Drifted: false}})
	if !strings.Contains(buf.String(), "No drift detected") {
		t.Errorf("got %q", buf.String())
	}
}

func TestPrintValidationDrift(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	var buf bytes.Buffer
	PrintValidation(&buf, []core.ValidationResult{{ResourceType: "widget", ResourceID: "a", Drifted: true, Detail: "missing"}})

	out := buf.String()
	if !strings.Contains(out, "widget.a: missing") || !strings.Contains(out, "1 resource(s) drifted") {
		t.Errorf("got %q", out)
	}
}
