package contacts

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/emersion/go-vcard"
	"github.com/google/uuid"
)

// ContactProvider abstracts a remote contact backend (e.g. Google).
type ContactProvider interface {
	FetchContacts() ([]vcard.Card, error)
	WriteContact(vcard.Card) error
	DeleteContact(uid string) error
}

// ContactManager handles local storage and provider syncing.
type ContactManager struct {
	provider    ContactProvider
	storagePath string
}

func NewContactManager(provider ContactProvider, dir string) (*ContactManager, error) {
	contactsDir := filepath.Join(dir, "people")
	if err := os.MkdirAll(contactsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create contacts directory: %w", err)
	}
	return &ContactManager{
		provider:    provider,
		storagePath: contactsDir,
	}, nil
}

// --- Helper functions for vcard.Card ---

// CardUID returns the UID from a vcard.Card.
func CardUID(card vcard.Card) string {
	if f := card.Value(vcard.FieldUID); f != "" {
		return f
	}
	return ""
}

// CardFullName returns the formatted name (FN) from a vcard.Card.
func CardFullName(card vcard.Card) string {
	return card.Value(vcard.FieldFormattedName)
}

// PrimaryPhone returns the first mobile/cell phone, or the first phone if none.
func PrimaryPhone(card vcard.Card) string {
	fields := card[vcard.FieldTelephone]
	if len(fields) == 0 {
		return ""
	}
	for _, f := range fields {
		t := strings.ToLower(f.Params.Get(vcard.ParamType))
		if t == "cell" || t == "mobile" {
			return f.Value
		}
	}
	return fields[0].Value
}

// PrimaryEmail returns the first email address.
func PrimaryEmail(card vcard.Card) string {
	fields := card[vcard.FieldEmail]
	if len(fields) == 0 {
		return ""
	}
	return fields[0].Value
}

// NewCard creates a minimal vcard.Card with a UID and FN.
func NewCard(fullName string) vcard.Card {
	card := make(vcard.Card)
	card.SetValue(vcard.FieldVersion, "4.0")
	card.SetValue(vcard.FieldUID, uuid.New().String())
	card.SetValue(vcard.FieldFormattedName, fullName)
	return card
}

// FormatCard returns a human-readable summary of a vcard.Card.
func FormatCard(card vcard.Card) string {
	var b strings.Builder

	// Name header
	if fn := CardFullName(card); fn != "" {
		b.WriteString(fn)
		b.WriteByte('\n')
		b.WriteString(strings.Repeat("-", len(fn)))
		b.WriteByte('\n')
	}

	// Nickname
	if nicks := card[vcard.FieldNickname]; len(nicks) > 0 {
		b.WriteString(fmt.Sprintf("  Nickname:  %s\n", nicks[0].Value))
	}

	// Organization + Title
	if org := card.Value(vcard.FieldOrganization); org != "" {
		display := strings.ReplaceAll(org, ";", ", ")
		display = strings.TrimRight(display, ", ")
		title := card.Value(vcard.FieldTitle)
		if title != "" {
			b.WriteString(fmt.Sprintf("  Work:      %s, %s\n", title, display))
		} else {
			b.WriteString(fmt.Sprintf("  Org:       %s\n", display))
		}
	} else if title := card.Value(vcard.FieldTitle); title != "" {
		b.WriteString(fmt.Sprintf("  Title:     %s\n", title))
	}

	// Phones
	for _, f := range card[vcard.FieldTelephone] {
		label := formatTypeLabel(f, "phone")
		b.WriteString(fmt.Sprintf("  Phone:     %s (%s)\n", f.Value, label))
	}

	// Emails
	for _, f := range card[vcard.FieldEmail] {
		label := formatTypeLabel(f, "email")
		b.WriteString(fmt.Sprintf("  Email:     %s (%s)\n", f.Value, label))
	}

	// Addresses
	for _, f := range card[vcard.FieldAddress] {
		label := formatTypeLabel(f, "address")
		addr := formatAddress(f.Value)
		if addr != "" {
			b.WriteString(fmt.Sprintf("  Address:   %s (%s)\n", addr, label))
		}
	}

	// Birthday
	if bday := card.Value(vcard.FieldBirthday); bday != "" {
		b.WriteString(fmt.Sprintf("  Birthday:  %s\n", formatDate(bday)))
	}

	// Anniversary
	if ann := card.Value(vcard.FieldAnniversary); ann != "" {
		b.WriteString(fmt.Sprintf("  Anniv:     %s\n", formatDate(ann)))
	}

	// URLs
	for _, f := range card[vcard.FieldURL] {
		label := formatTypeLabel(f, "url")
		b.WriteString(fmt.Sprintf("  URL:       %s (%s)\n", f.Value, label))
	}

	// IMPP
	for _, f := range card[vcard.FieldIMPP] {
		b.WriteString(fmt.Sprintf("  IM:        %s\n", f.Value))
	}

	// Relations
	for _, f := range card[vcard.FieldRelated] {
		label := formatTypeLabel(f, "related")
		b.WriteString(fmt.Sprintf("  Related:   %s (%s)\n", f.Value, label))
	}

	// Gender
	if g := card.Value(vcard.FieldGender); g != "" {
		b.WriteString(fmt.Sprintf("  Gender:    %s\n", g))
	}

	// Notes
	for _, f := range card[vcard.FieldNote] {
		b.WriteString(fmt.Sprintf("  Note:      %s\n", f.Value))
	}

	// X-GOOGLE-* extensions — show the interesting ones
	xFields := []struct {
		key   string
		label string
	}{
		{"X-GOOGLE-INTEREST", "Interest"},
		{"X-GOOGLE-SKILL", "Skill"},
		{"X-GOOGLE-OCCUPATION", "Occupation"},
		{"X-GOOGLE-LOCATION", "Location"},
	}
	for _, xf := range xFields {
		for _, f := range card[xf.key] {
			b.WriteString(fmt.Sprintf("  %s: %s%s\n", xf.label, strings.Repeat(" ", 9-len(xf.label)), f.Value))
		}
	}

	// UID footer
	if uid := CardUID(card); uid != "" {
		b.WriteString(fmt.Sprintf("  UID:       %s\n", uid))
	}

	return strings.TrimRight(b.String(), "\n")
}

func formatTypeLabel(f *vcard.Field, fallback string) string {
	if t := f.Params.Get(vcard.ParamType); t != "" {
		return t
	}
	return fallback
}

func formatAddress(adrValue string) string {
	// ADR: PO Box;Extended;Street;City;Region;PostalCode;Country
	parts := strings.Split(adrValue, ";")
	var pieces []string
	// Street (index 2)
	if len(parts) > 2 && parts[2] != "" {
		pieces = append(pieces, parts[2])
	}
	// City (index 3)
	if len(parts) > 3 && parts[3] != "" {
		pieces = append(pieces, parts[3])
	}
	// Region (index 4)
	if len(parts) > 4 && parts[4] != "" {
		pieces = append(pieces, parts[4])
	}
	// PostalCode (index 5)
	if len(parts) > 5 && parts[5] != "" {
		pieces = append(pieces, parts[5])
	}
	// Country (index 6)
	if len(parts) > 6 && parts[6] != "" {
		pieces = append(pieces, parts[6])
	}
	return strings.Join(pieces, ", ")
}

func formatDate(s string) string {
	// Try YYYYMMDD
	s = strings.ReplaceAll(s, "-", "")
	if len(s) == 8 {
		t, err := time.Parse("20060102", s)
		if err == nil {
			return t.Format("Jan 2, 2006")
		}
	}
	// Try --MMDD (no year)
	if len(s) == 4 {
		t, err := time.Parse("0102", s)
		if err == nil {
			return t.Format("Jan 2")
		}
	}
	return s
}

// EncodeCard serializes a vcard.Card to VCF bytes.
func EncodeCard(card vcard.Card) ([]byte, error) {
	var buf bytes.Buffer
	enc := vcard.NewEncoder(&buf)
	if err := enc.Encode(card); err != nil {
		return nil, fmt.Errorf("failed to encode vcard: %w", err)
	}
	return buf.Bytes(), nil
}

// DecodeCard deserializes VCF bytes into a vcard.Card.
func DecodeCard(data []byte) (vcard.Card, error) {
	dec := vcard.NewDecoder(bytes.NewReader(data))
	card, err := dec.Decode()
	if err != nil {
		return nil, fmt.Errorf("failed to decode vcard: %w", err)
	}
	return card, nil
}

// --- ContactManager methods ---

func (cm *ContactManager) GetContact(uid string) (vcard.Card, error) {
	filePath := filepath.Join(cm.storagePath, uid+".vcf")
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read contact file: %w", err)
	}
	card, err := DecodeCard(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse contact file: %w", err)
	}
	return card, nil
}

// FindContactByName searches contacts by name (case-insensitive exact match).
func (cm *ContactManager) FindContactByName(name string) (vcard.Card, error) {
	cards, err := cm.ListContacts()
	if err != nil {
		return nil, err
	}
	for _, card := range cards {
		if strings.EqualFold(CardFullName(card), name) {
			return card, nil
		}
	}
	return nil, nil
}

// ResolveContact looks up a contact by UID first, then falls back to name match.
func (cm *ContactManager) ResolveContact(query string) (vcard.Card, error) {
	card, err := cm.GetContact(query)
	if err != nil {
		return nil, err
	}
	if card != nil {
		return card, nil
	}
	return cm.FindContactByName(query)
}

func (cm *ContactManager) ListContacts() ([]vcard.Card, error) {
	entries, err := os.ReadDir(cm.storagePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read contacts directory: %w", err)
	}
	var cards []vcard.Card
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".vcf") {
			continue
		}
		filePath := filepath.Join(cm.storagePath, entry.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read contact file %s: %w", entry.Name(), err)
		}
		card, err := DecodeCard(data)
		if err != nil {
			return nil, fmt.Errorf("failed to parse contact file %s: %w", entry.Name(), err)
		}
		cards = append(cards, card)
	}
	return cards, nil
}

func (cm *ContactManager) WriteContact(card vcard.Card) error {
	if CardUID(card) == "" {
		card.SetValue(vcard.FieldUID, uuid.New().String())
	}
	card.SetValue(vcard.FieldRevision, time.Now().UTC().Format("20060102T150405Z"))

	data, err := EncodeCard(card)
	if err != nil {
		return fmt.Errorf("failed to marshal contact: %w", err)
	}
	filePath := filepath.Join(cm.storagePath, CardUID(card)+".vcf")
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write contact file: %w", err)
	}
	if cm.provider != nil {
		if err := cm.provider.WriteContact(card); err != nil {
			return fmt.Errorf("failed to write contact to provider: %w", err)
		}
	}
	return nil
}

func (cm *ContactManager) WriteContacts(cards []vcard.Card) error {
	for _, card := range cards {
		if err := cm.WriteContact(card); err != nil {
			return err
		}
	}
	return nil
}

func (cm *ContactManager) DeleteContact(uid string) error {
	isProviderContact := !strings.Contains(uid, "-")
	if isProviderContact && cm.provider != nil {
		if err := cm.provider.DeleteContact(uid); err != nil {
			return fmt.Errorf("failed to delete contact from provider: %w", err)
		}
	}
	filePath := filepath.Join(cm.storagePath, uid+".vcf")
	if err := os.Remove(filePath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("contact not found: %s", uid)
		}
		return fmt.Errorf("failed to delete contact: %w", err)
	}
	return nil
}

func (cm *ContactManager) SyncContacts() error {
	remoteContacts, err := cm.provider.FetchContacts()
	if err != nil {
		return fmt.Errorf("failed to fetch remote contacts: %w", err)
	}
	for _, card := range remoteContacts {
		if err := cm.writeContactLocal(card); err != nil {
			return fmt.Errorf("failed to write local contact: %w", err)
		}
	}
	return nil
}

func (cm *ContactManager) writeContactLocal(card vcard.Card) error {
	if CardUID(card) == "" {
		card.SetValue(vcard.FieldUID, uuid.New().String())
	}
	card.Set("X-LAST-SYNCED", &vcard.Field{
		Value: time.Now().UTC().Format("20060102T150405Z"),
	})

	data, err := EncodeCard(card)
	if err != nil {
		return fmt.Errorf("failed to marshal contact: %w", err)
	}
	filePath := filepath.Join(cm.storagePath, CardUID(card)+".vcf")
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write contact file: %w", err)
	}
	return nil
}
