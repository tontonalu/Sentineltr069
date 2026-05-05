package homologation

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	tplapp "github.com/celinet/sentinel-acs/internal/application/templates"
	hom "github.com/celinet/sentinel-acs/internal/domain/homologation"
	inv "github.com/celinet/sentinel-acs/internal/domain/inventory"
	tmpl "github.com/celinet/sentinel-acs/internal/domain/templates"
)

// fixture monta um Service inteiro com fakes + um device de lab
// (TR-181, com modelo). Devolve handles dos fakes para asserts.
type fixture struct {
	svc        *Service
	sessions   *fakeSessionRepo
	mappings   *fakeMappingRepo
	canonical  *fakeCanonicalRepo
	homModel   *fakeHomModelRepo
	devices    *fakeDeviceRepo
	models     *fakeModelRepo
	tplProfile *fakeTplProfileRepo
	tplParams  *fakeTplParamRepo
	tplHist    *fakeTplHistory
	genie      *fakeGenie

	// dados pré-populados
	labDevice inv.Device
	model     inv.DeviceModel
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	sessions := newFakeSessionRepo()
	mappings := newFakeMappingRepo()
	canonical := &fakeCanonicalRepo{}
	homModel := &fakeHomModelRepo{}
	devices := newFakeDeviceRepo()
	models := newFakeModelRepo()
	tplProfile := newFakeTplProfileRepo()
	tplParams := newFakeTplParamRepo()
	tplHist := &fakeTplHistory{}
	genie := newFakeGenie()

	tplSvc := tplapp.NewService(tplProfile, tplParams, tplHist)

	svc := NewService(sessions, mappings, canonical, homModel,
		devices, models, tplSvc, genie)

	model := inv.DeviceModel{
		ID:          uuid.New(),
		VendorID:    uuid.New(),
		Model:       "TestModel-1",
		TRDataModel: inv.TR181,
	}
	models.put(model)

	mid := model.ID
	labDevice := inv.Device{
		ID:                uuid.New(),
		GenieACSID:        "genie-lab-1",
		ModelID:           &mid,
		IsHomologationLab: true,
	}
	devices.put(labDevice)

	return &fixture{
		svc: svc, sessions: sessions, mappings: mappings, canonical: canonical,
		homModel: homModel, devices: devices, models: models,
		tplProfile: tplProfile, tplParams: tplParams, tplHist: tplHist, genie: genie,
		labDevice: labDevice, model: model,
	}
}

// ──────────── StartSession ────────────

func TestStartSession_OK(t *testing.T) {
	f := newFixture(t)
	sess, err := f.svc.StartSession(context.Background(), f.labDevice.ID, nil)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if sess.Status != hom.SessionDraft {
		t.Errorf("status=%q, want draft", sess.Status)
	}
	if sess.LabDeviceID != f.labDevice.ID || sess.ModelID != f.model.ID {
		t.Errorf("session linkagem errada: %+v", sess)
	}
}

func TestStartSession_RejectsNonLabDevice(t *testing.T) {
	f := newFixture(t)
	// device sem flag de lab
	plain := f.labDevice
	plain.ID = uuid.New()
	plain.IsHomologationLab = false
	f.devices.put(plain)

	_, err := f.svc.StartSession(context.Background(), plain.ID, nil)
	if !errors.Is(err, hom.ErrDeviceNotLab) {
		t.Errorf("err=%v, want ErrDeviceNotLab", err)
	}
}

func TestStartSession_RejectsDeviceWithoutModel(t *testing.T) {
	f := newFixture(t)
	noModel := f.labDevice
	noModel.ID = uuid.New()
	noModel.ModelID = nil
	f.devices.put(noModel)

	_, err := f.svc.StartSession(context.Background(), noModel.ID, nil)
	if !errors.Is(err, hom.ErrSessionMissingModel) {
		t.Errorf("err=%v, want ErrSessionMissingModel", err)
	}
}

func TestStartSession_BlocksConcurrentActiveSession(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.StartSession(context.Background(), f.labDevice.ID, nil); err != nil {
		t.Fatal(err)
	}
	_, err := f.svc.StartSession(context.Background(), f.labDevice.ID, nil)
	if !errors.Is(err, hom.ErrSessionAlreadyActive) {
		t.Errorf("err=%v, want ErrSessionAlreadyActive", err)
	}
}

// ──────────── AddMapping ────────────

func TestAddMapping_OK(t *testing.T) {
	f := newFixture(t)
	sess := mustStart(t, f)

	m, err := f.svc.AddMapping(context.Background(), AddMappingInput{
		SessionID:    sess.ID,
		CanonicalKey: "wifi.ssid.2g",
		TRPath:       "Device.WiFi.SSID.1.SSID",
		DataType:     tmpl.DataTypeString,
	})
	if err != nil {
		t.Fatalf("AddMapping: %v", err)
	}
	if m.ID == uuid.Nil || m.SessionID != sess.ID {
		t.Errorf("mapping inválido: %+v", m)
	}
}

func TestAddMapping_RejectsDuplicateCanonicalKey(t *testing.T) {
	f := newFixture(t)
	sess := mustStart(t, f)

	in := AddMappingInput{
		SessionID:    sess.ID,
		CanonicalKey: "wifi.ssid.2g",
		TRPath:       "Device.WiFi.SSID.1.SSID",
		DataType:     tmpl.DataTypeString,
	}
	if _, err := f.svc.AddMapping(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	_, err := f.svc.AddMapping(context.Background(), in)
	if !errors.Is(err, hom.ErrMappingDuplicate) {
		t.Errorf("err=%v, want ErrMappingDuplicate", err)
	}
}

func TestAddMapping_RejectsEmptyFields(t *testing.T) {
	f := newFixture(t)
	sess := mustStart(t, f)

	cases := []AddMappingInput{
		{SessionID: sess.ID, CanonicalKey: "", TRPath: "X", DataType: tmpl.DataTypeString},
		{SessionID: sess.ID, CanonicalKey: "X", TRPath: "", DataType: tmpl.DataTypeString},
		{SessionID: sess.ID, CanonicalKey: "X", TRPath: "Y", DataType: "garbage"},
	}
	for i, c := range cases {
		if _, err := f.svc.AddMapping(context.Background(), c); err == nil {
			t.Errorf("case %d: esperava erro, veio nil", i)
		}
	}
}

func TestRemoveMapping_OK(t *testing.T) {
	f := newFixture(t)
	sess := mustStart(t, f)
	m, err := f.svc.AddMapping(context.Background(), AddMappingInput{
		SessionID: sess.ID, CanonicalKey: "k", TRPath: "p", DataType: tmpl.DataTypeString,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.RemoveMapping(context.Background(), sess.ID, m.ID); err != nil {
		t.Fatalf("RemoveMapping: %v", err)
	}
	if _, err := f.mappings.GetByID(context.Background(), m.ID); !errors.Is(err, hom.ErrMappingNotFound) {
		t.Errorf("mapping ainda existe após remove")
	}
}

// ──────────── Complete ────────────

func TestComplete_OnlyEligibleMappingsBecomeProfileParameters(t *testing.T) {
	f := newFixture(t)
	sess := mustStartTesting(t, f)

	// 3 mappings: um OK/OK, um secret OK/skipped, um com fail no read.
	addMappingWithStatus(t, f, sess.ID, "wifi.ssid.2g", "Device.WiFi.SSID.1.SSID",
		tmpl.DataTypeString, false, hom.TestOK, hom.TestOK, "celinet")
	addMappingWithStatus(t, f, sess.ID, "wifi.password.2g", "Device.WiFi.AccessPoint.1.Security.KeyPassphrase",
		tmpl.DataTypeString, true, hom.TestOK, hom.TestSkipped, "secretpw")
	addMappingWithStatus(t, f, sess.ID, "broken.path", "Device.Does.Not.Exist",
		tmpl.DataTypeString, false, hom.TestFail, hom.TestPending, "")

	prof, err := f.svc.Complete(context.Background(), CompleteInput{
		SessionID:   sess.ID,
		ProfileName: "test_profile",
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(prof.Parameters) != 2 {
		t.Errorf("profile tem %d params, esperava 2 (somente eligible)", len(prof.Parameters))
	}
	keys := map[string]bool{}
	for _, p := range prof.Parameters {
		keys[p.CanonicalKey] = true
	}
	if !keys["wifi.ssid.2g"] || !keys["wifi.password.2g"] {
		t.Errorf("parâmetros eligible faltando: %v", keys)
	}
	if keys["broken.path"] {
		t.Errorf("mapping fail entrou no profile, não deveria")
	}

	// ModelHomologation deve ter sido criada
	homs, _ := f.homModel.ListByProfile(context.Background(), prof.ID)
	if len(homs) != 1 {
		t.Fatalf("esperava 1 ModelHomologation, veio %d", len(homs))
	}
	if homs[0].ModelID != f.model.ID || homs[0].Status != hom.StatusHomologated {
		t.Errorf("ModelHomologation incorreta: %+v", homs[0])
	}

	// Sessão deve ter virado completed + linkado o profile
	updated, _ := f.sessions.GetByID(context.Background(), sess.ID)
	if updated.Status != hom.SessionCompleted {
		t.Errorf("session status=%q, want completed", updated.Status)
	}
	if updated.GeneratedProfileID == nil || *updated.GeneratedProfileID != prof.ID {
		t.Errorf("session.GeneratedProfileID errado")
	}
}

func TestComplete_RefuseWhenNoEligibleMappings(t *testing.T) {
	f := newFixture(t)
	sess := mustStartTesting(t, f)
	addMappingWithStatus(t, f, sess.ID, "broken", "Device.Nope",
		tmpl.DataTypeString, false, hom.TestFail, hom.TestPending, "")

	_, err := f.svc.Complete(context.Background(), CompleteInput{
		SessionID:   sess.ID,
		ProfileName: "x",
	})
	if !errors.Is(err, hom.ErrNoEligibleMappings) {
		t.Errorf("err=%v, want ErrNoEligibleMappings", err)
	}
}

// ──────────── SuggestMappings (auto-map) ────────────

func TestSuggestMappings_BatesPathTR181(t *testing.T) {
	f := newFixture(t)
	sess := mustStartTesting(t, f)

	// Catálogo: 2 chaves — uma com hint que existe na árvore, uma com hint que não existe.
	f.canonical.keys = []hom.CanonicalKey{
		{
			ID: uuid.New(), Key: "wifi.ssid.2g", LabelPT: "SSID 2.4GHz",
			Category: hom.CategoryWiFi, SuggestedDataType: tmpl.DataTypeString,
			HintPathsTR181: []string{"Device.WiFi.SSID.1.SSID"},
			HintPathsTR098: []string{"InternetGatewayDevice.Old.SSID"},
		},
		{
			ID: uuid.New(), Key: "missing.foo", LabelPT: "Missing",
			Category: hom.CategoryOther, SuggestedDataType: tmpl.DataTypeString,
			HintPathsTR181: []string{"Device.Does.Not.Exist"},
		},
	}

	// Tree snapshot mínimo — só o path do SSID 2g.
	raw := map[string]any{
		"Device": map[string]any{
			"_object": true,
			"WiFi": map[string]any{
				"_object": true,
				"SSID": map[string]any{
					"_object": true,
					"1": map[string]any{
						"_object": true,
						"SSID": map[string]any{
							"_value":    "celinet-test",
							"_type":     "xsd:string",
							"_writable": true,
						},
					},
				},
			},
		},
	}
	snap, _ := json.Marshal(raw)
	if err := f.sessions.UpdateTreeSnapshot(context.Background(), sess.ID, snap); err != nil {
		t.Fatal(err)
	}

	res, err := f.svc.SuggestMappings(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("SuggestMappings: %v", err)
	}
	if len(res.Suggestions) != 1 || res.Suggestions[0].CanonicalKey != "wifi.ssid.2g" {
		t.Errorf("suggestions=%+v, esperava 1 com wifi.ssid.2g", res.Suggestions)
	}
	if res.Suggestions[0].TRPath != "Device.WiFi.SSID.1.SSID" {
		t.Errorf("TRPath=%q, esperava Device.WiFi.SSID.1.SSID", res.Suggestions[0].TRPath)
	}
	if len(res.Missing) != 1 || res.Missing[0] != "missing.foo" {
		t.Errorf("missing=%v, esperava [missing.foo]", res.Missing)
	}
}

func TestSuggestMappings_PulaCanonicalKeysJaMapeadas(t *testing.T) {
	f := newFixture(t)
	sess := mustStartTesting(t, f)

	f.canonical.keys = []hom.CanonicalKey{
		{
			ID: uuid.New(), Key: "wifi.ssid.2g", LabelPT: "SSID 2.4GHz",
			Category: hom.CategoryWiFi, SuggestedDataType: tmpl.DataTypeString,
			HintPathsTR181: []string{"Device.WiFi.SSID.1.SSID"},
		},
	}
	raw := map[string]any{
		"Device": map[string]any{"WiFi": map[string]any{"SSID": map[string]any{"1": map[string]any{
			"SSID": map[string]any{"_value": "x", "_type": "xsd:string"},
		}}}},
	}
	snap, _ := json.Marshal(raw)
	_ = f.sessions.UpdateTreeSnapshot(context.Background(), sess.ID, snap)

	// Pré-cria mapping → SuggestMappings deve pular.
	if _, err := f.svc.AddMapping(context.Background(), AddMappingInput{
		SessionID: sess.ID, CanonicalKey: "wifi.ssid.2g",
		TRPath: "Device.WiFi.SSID.1.SSID", DataType: tmpl.DataTypeString,
	}); err != nil {
		t.Fatal(err)
	}

	res, err := f.svc.SuggestMappings(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Suggestions) != 0 {
		t.Errorf("esperava 0 sugestões (já mapeado), veio %+v", res.Suggestions)
	}
	if len(res.Existing) != 1 || res.Existing[0] != "wifi.ssid.2g" {
		t.Errorf("existing=%v, esperava [wifi.ssid.2g]", res.Existing)
	}
}

// ──────────── helpers ────────────

func mustStart(t *testing.T, f *fixture) *hom.Session {
	t.Helper()
	sess, err := f.svc.StartSession(context.Background(), f.labDevice.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	return sess
}

// mustStartTesting cria a sessão e força status='testing' (skip do Probe real,
// que precisaria de NBI). Mantém a árvore vazia até o teste setar.
func mustStartTesting(t *testing.T, f *fixture) *hom.Session {
	t.Helper()
	sess := mustStart(t, f)
	if err := f.sessions.UpdateStatus(context.Background(), sess.ID, hom.SessionTesting); err != nil {
		t.Fatal(err)
	}
	updated, _ := f.sessions.GetByID(context.Background(), sess.ID)
	return updated
}

// ──────────── Probe ────────────

func TestProbe_LeArvoreEMarcaTesting(t *testing.T) {
	f := newFixture(t)
	sess := mustStart(t, f)

	// Popula o "device" no fake genie com uma árvore mínima.
	f.genie.putDevice(f.labDevice.GenieACSID, map[string]any{
		"Device": map[string]any{
			"WiFi": map[string]any{
				"SSID": map[string]any{
					"1": map[string]any{
						"SSID": map[string]any{
							"_value": "celinet-2g", "_type": "xsd:string", "_writable": true,
						},
					},
				},
			},
		},
	})

	updated, err := f.svc.Probe(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if updated.Status != hom.SessionTesting {
		t.Errorf("status=%q, want testing", updated.Status)
	}
	if len(updated.TreeSnapshot) == 0 {
		t.Error("tree_snapshot vazio após Probe")
	}
}

func TestProbe_RecoveryQuandoGetDeviceFalha(t *testing.T) {
	f := newFixture(t)
	sess := mustStart(t, f)
	// não populamos genie → GetDevice retorna ErrDeviceNotFound

	if _, err := f.svc.Probe(context.Background(), sess.ID); err == nil {
		t.Error("esperava erro do Probe")
	}
	// Sessão deve estar de volta a draft (não presa em probing).
	updated, _ := f.sessions.GetByID(context.Background(), sess.ID)
	if updated.Status != hom.SessionDraft {
		t.Errorf("status=%q após Probe falho, want draft (recovery)", updated.Status)
	}
}

// ──────────── RunReadTest ────────────

func TestRunReadTest_OKQuandoPathExiste(t *testing.T) {
	f := newFixture(t)
	sess := mustStartTesting(t, f)

	// Snapshot direto (sem passar pelo Probe).
	raw := map[string]any{
		"Device": map[string]any{"WiFi": map[string]any{"SSID": map[string]any{"1": map[string]any{
			"SSID": map[string]any{"_value": "celinet", "_type": "xsd:string"},
		}}}},
	}
	snap, _ := json.Marshal(raw)
	_ = f.sessions.UpdateTreeSnapshot(context.Background(), sess.ID, snap)

	m, _ := f.svc.AddMapping(context.Background(), AddMappingInput{
		SessionID: sess.ID, CanonicalKey: "wifi.ssid.2g",
		TRPath: "Device.WiFi.SSID.1.SSID", DataType: tmpl.DataTypeString,
	})
	got, err := f.svc.RunReadTest(context.Background(), m.ID)
	if err != nil {
		t.Fatalf("RunReadTest: %v", err)
	}
	if got.ReadStatus != hom.TestOK {
		t.Errorf("read_status=%q, want ok", got.ReadStatus)
	}
	if got.ReadValue == nil || *got.ReadValue != "celinet" {
		t.Errorf("read_value=%v, want celinet", got.ReadValue)
	}
}

func TestRunReadTest_FailQuandoPathFalta(t *testing.T) {
	f := newFixture(t)
	sess := mustStartTesting(t, f)
	_ = f.sessions.UpdateTreeSnapshot(context.Background(), sess.ID, []byte("{}"))

	m, _ := f.svc.AddMapping(context.Background(), AddMappingInput{
		SessionID: sess.ID, CanonicalKey: "missing",
		TRPath: "Device.Nope.X", DataType: tmpl.DataTypeString,
	})
	got, err := f.svc.RunReadTest(context.Background(), m.ID)
	if err != nil {
		t.Fatalf("RunReadTest: %v", err)
	}
	if got.ReadStatus != hom.TestFail {
		t.Errorf("read_status=%q, want fail", got.ReadStatus)
	}
}

func TestRunReadTest_RedactsSecretValue(t *testing.T) {
	f := newFixture(t)
	sess := mustStartTesting(t, f)

	raw := map[string]any{
		"Device": map[string]any{"WiFi": map[string]any{"SSID": map[string]any{"1": map[string]any{
			"KeyPassphrase": map[string]any{"_value": "supersecret", "_type": "xsd:string"},
		}}}},
	}
	snap, _ := json.Marshal(raw)
	_ = f.sessions.UpdateTreeSnapshot(context.Background(), sess.ID, snap)

	m, _ := f.svc.AddMapping(context.Background(), AddMappingInput{
		SessionID: sess.ID, CanonicalKey: "wifi.password.2g",
		TRPath:   "Device.WiFi.SSID.1.KeyPassphrase",
		DataType: tmpl.DataTypeString, IsSecret: true,
	})
	got, _ := f.svc.RunReadTest(context.Background(), m.ID)
	if got.ReadStatus != hom.TestOK {
		t.Errorf("read_status=%q, want ok", got.ReadStatus)
	}
	if got.ReadValue == nil || *got.ReadValue == "supersecret" {
		t.Errorf("read_value não foi redacted: %v", got.ReadValue)
	}
}

// ──────────── RunWriteTest ────────────

func TestRunWriteTest_SkipsSecret(t *testing.T) {
	f := newFixture(t)
	sess := mustStartTesting(t, f)
	m, _ := f.svc.AddMapping(context.Background(), AddMappingInput{
		SessionID: sess.ID, CanonicalKey: "wifi.password.2g", TRPath: "p",
		DataType: tmpl.DataTypeString, IsSecret: true,
	})
	got, err := f.svc.RunWriteTest(context.Background(), RunWriteTestInput{
		MappingID: m.ID, TestValue: "x",
	})
	if err != nil {
		t.Fatalf("RunWriteTest: %v", err)
	}
	if got.WriteStatus != hom.TestSkipped {
		t.Errorf("write_status=%q, want skipped (is_secret)", got.WriteStatus)
	}
	// Genie não deve ter sido chamado para Set.
	if len(f.genie.setCalls) != 0 {
		t.Errorf("SetParameterValues chamado mesmo com is_secret=true")
	}
}

func TestRunWriteTest_OKChamaSetParameterValues(t *testing.T) {
	f := newFixture(t)
	sess := mustStartTesting(t, f)
	m, _ := f.svc.AddMapping(context.Background(), AddMappingInput{
		SessionID: sess.ID, CanonicalKey: "wifi.ssid.2g",
		TRPath:   "Device.WiFi.SSID.1.SSID",
		DataType: tmpl.DataTypeString,
	})
	got, err := f.svc.RunWriteTest(context.Background(), RunWriteTestInput{
		MappingID: m.ID, TestValue: "test_HOM",
	})
	if err != nil {
		t.Fatalf("RunWriteTest: %v", err)
	}
	if got.WriteStatus != hom.TestOK {
		t.Errorf("write_status=%q, want ok", got.WriteStatus)
	}
	// Pelo menos uma chamada com o path certo.
	if len(f.genie.setCalls) == 0 {
		t.Fatal("SetParameterValues não foi chamado")
	}
	if f.genie.setCalls[0].Path != "Device.WiFi.SSID.1.SSID" {
		t.Errorf("path=%q, want Device.WiFi.SSID.1.SSID", f.genie.setCalls[0].Path)
	}
}

func TestRunWriteTest_RestoreEnviaValorOriginal(t *testing.T) {
	f := newFixture(t)
	sess := mustStartTesting(t, f)
	m, _ := f.svc.AddMapping(context.Background(), AddMappingInput{
		SessionID: sess.ID, CanonicalKey: "wifi.ssid.2g",
		TRPath:   "Device.WiFi.SSID.1.SSID",
		DataType: tmpl.DataTypeString,
	})
	// Pré-popula read_value para o restore funcionar.
	_ = f.mappings.UpdateReadResult(context.Background(), m.ID, hom.TestOK, "originalSSID", "")

	if _, err := f.svc.RunWriteTest(context.Background(), RunWriteTestInput{
		MappingID: m.ID, TestValue: "test_HOM", RestoreOriginal: true,
	}); err != nil {
		t.Fatal(err)
	}
	// Deve ter 2 calls: test_HOM e depois originalSSID.
	if len(f.genie.setCalls) != 2 {
		t.Fatalf("setCalls=%d, want 2 (test + restore)", len(f.genie.setCalls))
	}
	if v, _ := f.genie.setCalls[1].Value.(string); v != "originalSSID" {
		t.Errorf("segundo Set value=%v, want originalSSID", f.genie.setCalls[1].Value)
	}
}

// ──────────── ApplyAutoMap ────────────

func TestApplyAutoMap_CriaMappingsParaSugestoes(t *testing.T) {
	f := newFixture(t)
	sess := mustStartTesting(t, f)

	suggestions := []AutoMapSuggestion{
		{CanonicalKey: "wifi.ssid.2g", TRPath: "Device.WiFi.SSID.1.SSID", DataType: "string"},
		{CanonicalKey: "wifi.password.2g", TRPath: "Device.WiFi.AccessPoint.1.Security.KeyPassphrase",
			DataType: "string", IsSecret: true},
	}
	created, err := f.svc.ApplyAutoMap(context.Background(), sess.ID, suggestions)
	if err != nil {
		t.Fatalf("ApplyAutoMap: %v", err)
	}
	if created != 2 {
		t.Errorf("created=%d, want 2", created)
	}
	got, _ := f.mappings.ListBySession(context.Background(), sess.ID)
	if len(got) != 2 {
		t.Errorf("mappings=%d, want 2", len(got))
	}
}

func TestApplyAutoMap_PulaDuplicatasSilenciosamente(t *testing.T) {
	f := newFixture(t)
	sess := mustStartTesting(t, f)
	// Pré-cria um mapping com a mesma canonical_key
	if _, err := f.svc.AddMapping(context.Background(), AddMappingInput{
		SessionID: sess.ID, CanonicalKey: "wifi.ssid.2g",
		TRPath: "any", DataType: tmpl.DataTypeString,
	}); err != nil {
		t.Fatal(err)
	}
	suggestions := []AutoMapSuggestion{
		{CanonicalKey: "wifi.ssid.2g", TRPath: "Device.WiFi.SSID.1.SSID", DataType: "string"},
		{CanonicalKey: "wifi.ssid.5g", TRPath: "Device.WiFi.SSID.2.SSID", DataType: "string"},
	}
	created, err := f.svc.ApplyAutoMap(context.Background(), sess.ID, suggestions)
	if err != nil {
		t.Fatalf("ApplyAutoMap: %v", err)
	}
	if created != 1 {
		t.Errorf("created=%d, want 1 (a duplicata foi pulada)", created)
	}
}

// ──────────── UpdateMappingTemplate ────────────

func TestUpdateMappingTemplate_OK(t *testing.T) {
	f := newFixture(t)
	sess := mustStartTesting(t, f)
	m, _ := f.svc.AddMapping(context.Background(), AddMappingInput{
		SessionID: sess.ID, CanonicalKey: "k", TRPath: "p", DataType: tmpl.DataTypeString,
	})
	if err := f.svc.UpdateMappingTemplate(context.Background(), sess.ID, m.ID, "{{ customer.pppoe_login }}"); err != nil {
		t.Fatalf("UpdateMappingTemplate: %v", err)
	}
	got, _ := f.mappings.GetByID(context.Background(), m.ID)
	if got.ValueTemplate != "{{ customer.pppoe_login }}" {
		t.Errorf("template=%q, want {{ customer.pppoe_login }}", got.ValueTemplate)
	}
}

// addMappingWithStatus cria mapping e força os status de read/write — usado
// para testar Complete sem ter que rodar SetParameterValues no NBI fake.
func addMappingWithStatus(t *testing.T, f *fixture, sessID uuid.UUID, ck, path string,
	dt tmpl.DataType, secret bool, read, write hom.TestStatus, readVal string,
) {
	t.Helper()
	m, err := f.svc.AddMapping(context.Background(), AddMappingInput{
		SessionID:    sessID,
		CanonicalKey: ck,
		TRPath:       path,
		DataType:     dt,
		IsSecret:     secret,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.mappings.UpdateReadResult(context.Background(), m.ID, read, readVal, ""); err != nil {
		t.Fatal(err)
	}
	if err := f.mappings.UpdateWriteResult(context.Background(), m.ID, write, "", ""); err != nil {
		t.Fatal(err)
	}
}
