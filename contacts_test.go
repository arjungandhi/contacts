package contacts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emersion/go-vcard"
)

func TestPrimaryPhone(t *testing.T) {
	tests := []struct {
		name   string
		phones []*vcard.Field
		want   string
	}{
		{"empty", nil, ""},
		{"single", []*vcard.Field{
			{Value: "555-1234", Params: vcard.Params{vcard.ParamType: []string{"home"}}},
		}, "555-1234"},
		{"prefers mobile", []*vcard.Field{
			{Value: "555-1234", Params: vcard.Params{vcard.ParamType: []string{"home"}}},
			{Value: "555-5678", Params: vcard.Params{vcard.ParamType: []string{"mobile"}}},
		}, "555-5678"},
		{"prefers cell", []*vcard.Field{
			{Value: "555-1234", Params: vcard.Params{vcard.ParamType: []string{"work"}}},
			{Value: "555-9999", Params: vcard.Params{vcard.ParamType: []string{"cell"}}},
		}, "555-9999"},
		{"fallback to first", []*vcard.Field{
			{Value: "555-1111", Params: vcard.Params{vcard.ParamType: []string{"work"}}},
			{Value: "555-2222", Params: vcard.Params{vcard.ParamType: []string{"home"}}},
		}, "555-1111"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			card := make(vcard.Card)
			card.SetValue(vcard.FieldVersion, "4.0")
			for _, f := range tt.phones {
				card.Add(vcard.FieldTelephone, f)
			}
			if got := PrimaryPhone(card); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPrimaryEmail(t *testing.T) {
	tests := []struct {
		name   string
		emails []*vcard.Field
		want   string
	}{
		{"empty", nil, ""},
		{"single", []*vcard.Field{
			{Value: "a@b.com", Params: vcard.Params{vcard.ParamType: []string{"home"}}},
		}, "a@b.com"},
		{"returns first", []*vcard.Field{
			{Value: "first@b.com", Params: vcard.Params{vcard.ParamType: []string{"work"}}},
			{Value: "second@b.com", Params: vcard.Params{vcard.ParamType: []string{"home"}}},
		}, "first@b.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			card := make(vcard.Card)
			card.SetValue(vcard.FieldVersion, "4.0")
			for _, f := range tt.emails {
				card.Add(vcard.FieldEmail, f)
			}
			if got := PrimaryEmail(card); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestContactManager_CRUD(t *testing.T) {
	dir := t.TempDir()
	cm, err := NewContactManager(nil, dir)
	if err != nil {
		t.Fatal(err)
	}

	// List empty
	cards, err := cm.ListContacts()
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 0 {
		t.Fatalf("expected 0 contacts, got %d", len(cards))
	}

	// Write
	card := make(vcard.Card)
	card.SetValue(vcard.FieldVersion, "4.0")
	card.SetValue(vcard.FieldUID, "test-uid-1")
	card.SetValue(vcard.FieldFormattedName, "Alice Smith")
	card.Add(vcard.FieldTelephone, &vcard.Field{
		Value:  "555-1234",
		Params: vcard.Params{vcard.ParamType: []string{"mobile"}},
	})
	card.Add(vcard.FieldEmail, &vcard.Field{
		Value:  "alice@example.com",
		Params: vcard.Params{vcard.ParamType: []string{"home"}},
	})
	if err := cm.WriteContact(card); err != nil {
		t.Fatal(err)
	}

	// Get
	got, err := cm.GetContact("test-uid-1")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected contact, got nil")
	}
	if CardFullName(got) != "Alice Smith" {
		t.Errorf("got %q, want %q", CardFullName(got), "Alice Smith")
	}
	if got.Value(vcard.FieldRevision) == "" {
		t.Error("expected REV to be set")
	}

	// List
	cards, err = cm.ListContacts()
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 {
		t.Fatalf("expected 1 contact, got %d", len(cards))
	}

	// Get nonexistent
	got, err = cm.GetContact("nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatal("expected nil for nonexistent contact")
	}

	// Delete
	if err := cm.DeleteContact("test-uid-1"); err != nil {
		t.Fatal(err)
	}
	cards, err = cm.ListContacts()
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 0 {
		t.Fatalf("expected 0 contacts after delete, got %d", len(cards))
	}

	// Delete nonexistent
	if err := cm.DeleteContact("nonexistent"); err == nil {
		t.Error("expected error deleting nonexistent contact")
	}
}

func TestContactManager_WriteGeneratesUID(t *testing.T) {
	dir := t.TempDir()
	cm, err := NewContactManager(nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	card := make(vcard.Card)
	card.SetValue(vcard.FieldVersion, "4.0")
	card.SetValue(vcard.FieldFormattedName, "Bob")
	if err := cm.WriteContact(card); err != nil {
		t.Fatal(err)
	}
	cards, err := cm.ListContacts()
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 {
		t.Fatal("expected 1 contact")
	}
	if CardUID(cards[0]) == "" {
		t.Error("expected UID to be generated")
	}
}

type mockProvider struct {
	contacts []vcard.Card
}

func (m *mockProvider) FetchContacts() ([]vcard.Card, error) {
	return m.contacts, nil
}
func (m *mockProvider) WriteContact(c vcard.Card) error { return nil }
func (m *mockProvider) DeleteContact(uid string) error   { return nil }

func TestContactManager_SyncContacts(t *testing.T) {
	dir := t.TempDir()
	c1 := make(vcard.Card)
	c1.SetValue(vcard.FieldVersion, "4.0")
	c1.SetValue(vcard.FieldUID, "sync-1")
	c1.SetValue(vcard.FieldFormattedName, "Synced One")
	c2 := make(vcard.Card)
	c2.SetValue(vcard.FieldVersion, "4.0")
	c2.SetValue(vcard.FieldUID, "sync-2")
	c2.SetValue(vcard.FieldFormattedName, "Synced Two")

	provider := &mockProvider{contacts: []vcard.Card{c1, c2}}
	cm, err := NewContactManager(provider, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := cm.SyncContacts(); err != nil {
		t.Fatal(err)
	}
	cards, err := cm.ListContacts()
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 2 {
		t.Fatalf("expected 2 contacts, got %d", len(cards))
	}

	// Verify synced contacts have X-LAST-SYNCED set
	for _, c := range cards {
		if c.Value("X-LAST-SYNCED") == "" {
			t.Errorf("contact %s: expected X-LAST-SYNCED to be set", CardUID(c))
		}
	}
}

func TestContactManager_WriteContactVCF(t *testing.T) {
	dir := t.TempDir()
	cm, err := NewContactManager(nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	card := make(vcard.Card)
	card.SetValue(vcard.FieldVersion, "4.0")
	card.SetValue(vcard.FieldUID, "vcf-test")
	card.SetValue(vcard.FieldFormattedName, "VCF Test")
	card.SetValue(vcard.FieldOrganization, "TestCorp;Engineering")
	card.SetValue(vcard.FieldTitle, "Engineer")
	if err := cm.WriteContact(card); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "people", "vcf-test.vcf"))
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := DecodeCard(data)
	if err != nil {
		t.Fatal(err)
	}
	org := loaded.Value(vcard.FieldOrganization)
	if !strings.Contains(org, "TestCorp") {
		t.Error("organization not preserved in VCF")
	}
	if loaded.Value(vcard.FieldTitle) != "Engineer" {
		t.Error("title not preserved in VCF")
	}
}

func TestFormatCard(t *testing.T) {
	card := make(vcard.Card)
	card.SetValue(vcard.FieldVersion, "4.0")
	card.SetValue(vcard.FieldUID, "fmt-test")
	card.SetValue(vcard.FieldFormattedName, "Alice Smith")
	card.Add(vcard.FieldTelephone, &vcard.Field{
		Value:  "555-1234",
		Params: vcard.Params{vcard.ParamType: []string{"mobile"}},
	})
	card.Add(vcard.FieldEmail, &vcard.Field{
		Value:  "alice@example.com",
		Params: vcard.Params{vcard.ParamType: []string{"work"}},
	})
	card.SetValue(vcard.FieldOrganization, "Acme Inc;Engineering")
	card.SetValue(vcard.FieldTitle, "Engineer")
	card.SetValue(vcard.FieldBirthday, "19900615")
	card.Add(vcard.FieldNote, &vcard.Field{Value: "Best friend"})
	card.Add(vcard.FieldAddress, &vcard.Field{
		Value:  ";;123 Main St;Springfield;IL;62701;US",
		Params: vcard.Params{vcard.ParamType: []string{"home"}},
	})

	out := FormatCard(card)

	checks := []string{
		"Alice Smith",
		"555-1234",
		"alice@example.com",
		"Engineer",
		"Acme Inc",
		"Jun 15, 1990",
		"Best friend",
		"123 Main St",
		"Springfield",
		"fmt-test",
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("FormatCard missing %q in output:\n%s", want, out)
		}
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	card := NewCard("Round Trip Test")
	card.Add(vcard.FieldTelephone, &vcard.Field{
		Value:  "555-0000",
		Params: vcard.Params{vcard.ParamType: []string{"cell"}},
	})
	card.Add(vcard.FieldEmail, &vcard.Field{
		Value:  "test@example.com",
		Params: vcard.Params{vcard.ParamType: []string{"work"}},
	})
	card.SetValue(vcard.FieldNote, "A note")

	data, err := EncodeCard(card)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeCard(data)
	if err != nil {
		t.Fatal(err)
	}
	if CardFullName(decoded) != "Round Trip Test" {
		t.Errorf("FN: got %q, want %q", CardFullName(decoded), "Round Trip Test")
	}
	if PrimaryPhone(decoded) != "555-0000" {
		t.Errorf("phone: got %q, want %q", PrimaryPhone(decoded), "555-0000")
	}
	if PrimaryEmail(decoded) != "test@example.com" {
		t.Errorf("email: got %q, want %q", PrimaryEmail(decoded), "test@example.com")
	}
	if decoded.Value(vcard.FieldNote) != "A note" {
		t.Errorf("note: got %q, want %q", decoded.Value(vcard.FieldNote), "A note")
	}
}
