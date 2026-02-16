package contacts

import (
	"strings"
	"testing"

	"github.com/emersion/go-vcard"
)

func TestConvertPeopleAPIToCard(t *testing.T) {
	person := peopleAPIPerson{
		ResourceName: "people/c123456",
		ETag:         "etag123",
		Names: []peopleAPIName{
			{DisplayName: "John Doe", GivenName: "John", FamilyName: "Doe", MiddleName: "M", HonorificPrefix: "Dr", HonorificSuffix: "Jr"},
		},
		Nicknames: []peopleAPINickname{
			{Value: "Johnny"},
		},
		PhoneNumbers: []peopleAPIPhoneNumber{
			{Value: "555-1234", Type: "mobile"},
			{Value: "555-5678", Type: "work"},
		},
		EmailAddresses: []peopleAPIEmailAddress{
			{Value: "john@example.com", Type: "home"},
		},
		Addresses: []peopleAPIAddress{
			{StreetAddress: "123 Main St", City: "Springfield", Region: "IL", PostalCode: "62701", Country: "US", Type: "home"},
		},
		Organizations: []peopleAPIOrganization{
			{Name: "Acme Inc", Title: "Engineer", Department: "R&D"},
		},
		Birthdays: []peopleAPIBirthday{
			{Date: struct {
				Year  int `json:"year"`
				Month int `json:"month"`
				Day   int `json:"day"`
			}{Year: 1990, Month: 6, Day: 15}},
		},
		Photos: []peopleAPIPhoto{
			{URL: "https://photo.example.com/john.jpg"},
		},
		Biographies: []peopleAPIBiography{
			{Value: "Some notes about John"},
		},
		URLs: []peopleAPIURL{
			{Value: "https://johndoe.com", Type: "blog"},
		},
		Events: []peopleAPIEvent{
			{Date: struct {
				Year  int `json:"year"`
				Month int `json:"month"`
				Day   int `json:"day"`
			}{Year: 2020, Month: 1, Day: 1}, Type: "anniversary"},
			{Date: struct {
				Year  int `json:"year"`
				Month int `json:"month"`
				Day   int `json:"day"`
			}{Year: 2015, Month: 3, Day: 10}, Type: "other"},
		},
		Genders: []peopleAPIGender{
			{Value: "male"},
		},
		ImClients: []peopleAPIImClient{
			{Username: "johndoe", Protocol: "xmpp", Type: "home"},
		},
		Relations: []peopleAPIRelation{
			{Person: "Jane Doe", Type: "spouse"},
		},
		CalendarURLs: []peopleAPICalendarURL{
			{URL: "https://calendar.example.com/john", Type: "home"},
		},
		SipAddresses: []peopleAPISipAddress{
			{Value: "john@sip.example.com", Type: "work"},
		},
		Locales: []peopleAPILocale{
			{Value: "en-US"},
		},
		Interests: []peopleAPIInterest{
			{Value: "Coding"},
		},
		Skills: []peopleAPISkill{
			{Value: "Go"},
		},
		Occupations: []peopleAPIOccupation{
			{Value: "Software Developer"},
		},
		Locations: []peopleAPILocation{
			{Value: "Building A", Type: "desk"},
		},
		Memberships: []peopleAPIMembership{
			{ContactGroupMembership: &struct {
				ContactGroupResourceName string `json:"contactGroupResourceName"`
			}{ContactGroupResourceName: "contactGroups/friends"}},
		},
		UserDefined: []peopleAPIUserDefined{
			{Key: "Shirt Size", Value: "L"},
		},
		ClientData: []peopleAPIClientData{
			{Key: "app-id", Value: "12345"},
		},
		ExternalIds: []peopleAPIExternalId{
			{Value: "EMP001", Type: "organization"},
		},
		MiscKeywords: []peopleAPIMiscKeyword{
			{Value: "VIP", Type: "outlook"},
		},
		CoverPhotos: []peopleAPICoverPhoto{
			{URL: "https://cover.example.com/john.jpg"},
		},
		AgeRanges: []peopleAPIAgeRange{
			{AgeRange: "TWENTY_ONE_OR_OLDER"},
		},
		Metadata: &peopleAPIPersonMetadata{
			Sources: []struct {
				Type string `json:"type"`
				ID   string `json:"id"`
			}{
				{Type: "CONTACT", ID: "abc123"},
			},
		},
	}

	card := convertPeopleAPIToCard(person)

	// Basic fields
	if CardUID(card) != "c123456" {
		t.Errorf("UID: got %q, want %q", CardUID(card), "c123456")
	}
	if card.Value("X-GOOGLE-ETAG") != "etag123" {
		t.Errorf("ETag: got %q, want %q", card.Value("X-GOOGLE-ETAG"), "etag123")
	}
	if CardFullName(card) != "John Doe" {
		t.Errorf("FN: got %q, want %q", CardFullName(card), "John Doe")
	}

	// N field
	nFields := card[vcard.FieldName]
	if len(nFields) == 0 {
		t.Fatal("N field missing")
	}
	nParts := strings.SplitN(nFields[0].Value, ";", 5)
	if nParts[0] != "Doe" {
		t.Errorf("N family: got %q, want %q", nParts[0], "Doe")
	}
	if nParts[1] != "John" {
		t.Errorf("N given: got %q, want %q", nParts[1], "John")
	}

	// Nickname
	if nickFields := card[vcard.FieldNickname]; len(nickFields) == 0 || nickFields[0].Value != "Johnny" {
		t.Error("NICKNAME missing or wrong")
	}

	// Phone
	if tels := card[vcard.FieldTelephone]; len(tels) != 2 {
		t.Fatalf("TEL: got %d, want 2", len(tels))
	} else if tels[0].Value != "555-1234" {
		t.Errorf("TEL[0]: got %q, want %q", tels[0].Value, "555-1234")
	}

	// Email
	if emails := card[vcard.FieldEmail]; len(emails) != 1 {
		t.Fatalf("EMAIL: got %d, want 1", len(emails))
	}

	// Address
	if adrs := card[vcard.FieldAddress]; len(adrs) != 1 {
		t.Fatalf("ADR: got %d, want 1", len(adrs))
	} else if !strings.Contains(adrs[0].Value, "Springfield") {
		t.Errorf("ADR missing city, got %q", adrs[0].Value)
	}

	// Org + Title
	org := card.Value(vcard.FieldOrganization)
	if !strings.Contains(org, "Acme Inc") {
		t.Errorf("ORG: got %q", org)
	}
	if card.Value(vcard.FieldTitle) != "Engineer" {
		t.Errorf("TITLE: got %q", card.Value(vcard.FieldTitle))
	}

	// Birthday
	if card.Value(vcard.FieldBirthday) != "19900615" {
		t.Errorf("BDAY: got %q, want %q", card.Value(vcard.FieldBirthday), "19900615")
	}

	// Photo
	if photos := card[vcard.FieldPhoto]; len(photos) == 0 || photos[0].Value != "https://photo.example.com/john.jpg" {
		t.Error("PHOTO missing or wrong")
	}

	// Note
	if card.Value(vcard.FieldNote) != "Some notes about John" {
		t.Errorf("NOTE: got %q", card.Value(vcard.FieldNote))
	}

	// URL
	if urls := card[vcard.FieldURL]; len(urls) == 0 || urls[0].Value != "https://johndoe.com" {
		t.Error("URL missing or wrong")
	}

	// Anniversary (from events)
	if card.Value(vcard.FieldAnniversary) != "20200101" {
		t.Errorf("ANNIVERSARY: got %q, want %q", card.Value(vcard.FieldAnniversary), "20200101")
	}

	// Non-anniversary event → X-GOOGLE-EVENT
	if events := card["X-GOOGLE-EVENT"]; len(events) == 0 {
		t.Error("X-GOOGLE-EVENT missing")
	} else if events[0].Value != "20150310" {
		t.Errorf("X-GOOGLE-EVENT: got %q, want %q", events[0].Value, "20150310")
	}

	// Gender
	if card.Value(vcard.FieldGender) != "male" {
		t.Errorf("GENDER: got %q", card.Value(vcard.FieldGender))
	}

	// IMPP (from imClients)
	imppFields := card[vcard.FieldIMPP]
	if len(imppFields) < 1 {
		t.Fatal("IMPP missing")
	}
	if imppFields[0].Value != "xmpp:johndoe" {
		t.Errorf("IMPP[0]: got %q, want %q", imppFields[0].Value, "xmpp:johndoe")
	}

	// IMPP (from sipAddresses — should be second IMPP)
	foundSip := false
	for _, f := range imppFields {
		if f.Value == "sip:john@sip.example.com" {
			foundSip = true
		}
	}
	if !foundSip {
		t.Error("SIP address not found in IMPP fields")
	}

	// RELATED
	if rels := card[vcard.FieldRelated]; len(rels) == 0 || rels[0].Value != "Jane Doe" {
		t.Error("RELATED missing or wrong")
	}

	// CALURI
	if cals := card[vcard.FieldCalendarURI]; len(cals) == 0 || cals[0].Value != "https://calendar.example.com/john" {
		t.Error("CALURI missing or wrong")
	}

	// LANG
	if langs := card[vcard.FieldLanguage]; len(langs) == 0 || langs[0].Value != "en-US" {
		t.Error("LANG missing or wrong")
	}

	// X-GOOGLE-* extensions
	if v := card["X-GOOGLE-INTEREST"]; len(v) == 0 || v[0].Value != "Coding" {
		t.Error("X-GOOGLE-INTEREST missing or wrong")
	}
	if v := card["X-GOOGLE-SKILL"]; len(v) == 0 || v[0].Value != "Go" {
		t.Error("X-GOOGLE-SKILL missing or wrong")
	}
	if v := card["X-GOOGLE-OCCUPATION"]; len(v) == 0 || v[0].Value != "Software Developer" {
		t.Error("X-GOOGLE-OCCUPATION missing or wrong")
	}
	if v := card["X-GOOGLE-LOCATION"]; len(v) == 0 || v[0].Value != "Building A" {
		t.Error("X-GOOGLE-LOCATION missing or wrong")
	}
	if v := card["X-GOOGLE-GROUP-MEMBERSHIP"]; len(v) == 0 || v[0].Value != "contactGroups/friends" {
		t.Error("X-GOOGLE-GROUP-MEMBERSHIP missing or wrong")
	}
	if v := card["X-GOOGLE-CUSTOM-SHIRT-SIZE"]; len(v) == 0 || v[0].Value != "L" {
		t.Error("X-GOOGLE-CUSTOM-SHIRT-SIZE missing or wrong")
	}
	if v := card["X-GOOGLE-CLIENT-APP-ID"]; len(v) == 0 || v[0].Value != "12345" {
		t.Error("X-GOOGLE-CLIENT-APP-ID missing or wrong")
	}
	if v := card["X-GOOGLE-EXTERNAL-ID"]; len(v) == 0 || v[0].Value != "EMP001" {
		t.Error("X-GOOGLE-EXTERNAL-ID missing or wrong")
	}
	if v := card["X-GOOGLE-KEYWORD"]; len(v) == 0 || v[0].Value != "VIP" {
		t.Error("X-GOOGLE-KEYWORD missing or wrong")
	}
	if v := card["X-GOOGLE-COVER-PHOTO"]; len(v) == 0 || v[0].Value != "https://cover.example.com/john.jpg" {
		t.Error("X-GOOGLE-COVER-PHOTO missing or wrong")
	}
	if v := card["X-GOOGLE-AGE-RANGE"]; len(v) == 0 || v[0].Value != "TWENTY_ONE_OR_OLDER" {
		t.Error("X-GOOGLE-AGE-RANGE missing or wrong")
	}
	if v := card["X-GOOGLE-SOURCE"]; len(v) == 0 || v[0].Value != "abc123" {
		t.Error("X-GOOGLE-SOURCE missing or wrong")
	}
}

func TestConvertCardToPeopleAPI(t *testing.T) {
	card := make(vcard.Card)
	card.SetValue(vcard.FieldVersion, "4.0")
	card.SetValue(vcard.FieldFormattedName, "Jane Smith")
	card[vcard.FieldName] = []*vcard.Field{
		{Value: "Smith;Jane;;;"},
	}
	card.Add(vcard.FieldTelephone, &vcard.Field{
		Value:  "555-9999",
		Params: vcard.Params{vcard.ParamType: []string{"mobile"}},
	})
	card.Add(vcard.FieldEmail, &vcard.Field{
		Value:  "jane@example.com",
		Params: vcard.Params{vcard.ParamType: []string{"work"}},
	})
	card.SetValue(vcard.FieldOrganization, "Corp")
	card.SetValue(vcard.FieldTitle, "CEO")
	card.SetValue(vcard.FieldBirthday, "19900615")
	card.Add(vcard.FieldNote, &vcard.Field{Value: "test note"})
	card.Add(vcard.FieldURL, &vcard.Field{
		Value:  "https://jane.dev",
		Params: vcard.Params{vcard.ParamType: []string{"blog"}},
	})

	result := convertCardToPeopleAPI(card)

	if result["names"] == nil {
		t.Fatal("names missing")
	}
	if result["phoneNumbers"] == nil {
		t.Fatal("phoneNumbers missing")
	}
	if result["emailAddresses"] == nil {
		t.Fatal("emailAddresses missing")
	}
	if result["organizations"] == nil {
		t.Fatal("organizations missing")
	}
	if result["birthdays"] == nil {
		t.Fatal("birthdays missing")
	}
	if result["biographies"] == nil {
		t.Fatal("biographies missing")
	}
	if result["urls"] == nil {
		t.Fatal("urls missing")
	}
}

func TestConvertPeopleAPIToCard_EmptyPerson(t *testing.T) {
	person := peopleAPIPerson{ResourceName: "people/empty"}
	card := convertPeopleAPIToCard(person)
	if CardUID(card) != "empty" {
		t.Errorf("UID: got %q, want %q", CardUID(card), "empty")
	}
	// FN should default to UID when no name present
	if CardFullName(card) != "empty" {
		t.Errorf("FN should default to UID, got %q", CardFullName(card))
	}
}

func TestConvertPeopleAPIRoundTrip(t *testing.T) {
	person := peopleAPIPerson{
		ResourceName: "people/rt123",
		Names: []peopleAPIName{
			{DisplayName: "Round Trip", GivenName: "Round", FamilyName: "Trip"},
		},
		PhoneNumbers: []peopleAPIPhoneNumber{
			{Value: "555-0000", Type: "mobile"},
		},
		EmailAddresses: []peopleAPIEmailAddress{
			{Value: "rt@example.com", Type: "work"},
		},
		Organizations: []peopleAPIOrganization{
			{Name: "RT Corp", Title: "Dev", Department: "Eng"},
		},
		Birthdays: []peopleAPIBirthday{
			{Date: struct {
				Year  int `json:"year"`
				Month int `json:"month"`
				Day   int `json:"day"`
			}{Year: 1985, Month: 12, Day: 25}},
		},
		Biographies: []peopleAPIBiography{
			{Value: "round trip test"},
		},
	}

	card := convertPeopleAPIToCard(person)
	result := convertCardToPeopleAPI(card)

	// Verify key fields survive round trip
	if result["names"] == nil {
		t.Fatal("names lost in round trip")
	}
	if result["phoneNumbers"] == nil {
		t.Fatal("phoneNumbers lost in round trip")
	}
	if result["emailAddresses"] == nil {
		t.Fatal("emailAddresses lost in round trip")
	}
	if result["organizations"] == nil {
		t.Fatal("organizations lost in round trip")
	}
	if result["birthdays"] == nil {
		t.Fatal("birthdays lost in round trip")
	}
	if result["biographies"] == nil {
		t.Fatal("biographies lost in round trip")
	}
}

func TestGeneratePKCE(t *testing.T) {
	verifier, challenge, err := generatePKCE()
	if err != nil {
		t.Fatal(err)
	}
	if verifier == "" {
		t.Error("verifier is empty")
	}
	if challenge == "" {
		t.Error("challenge is empty")
	}
	if verifier == challenge {
		t.Error("verifier and challenge should differ")
	}
}
