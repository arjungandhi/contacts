package contacts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/emersion/go-vcard"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

//go:embed assets/logo.svg
var logoSVG string

// allPersonFields lists every personField the People API supports.
const allPersonFields = "addresses,ageRanges,biographies,birthdays,calendarUrls,clientData,coverPhotos,emailAddresses,events,externalIds,genders,imClients,interests,locales,locations,memberships,metadata,miscKeywords,names,nicknames,occupations,organizations,phoneNumbers,photos,relations,sipAddresses,skills,urls,userDefined"

type GoogleCredentials struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	RefreshToken string `json:"refresh_token,omitempty"`
	AccessToken  string `json:"access_token,omitempty"`
	Email        string `json:"email,omitempty"`
}

type GoogleContactsProvider struct {
	config        *oauth2.Config
	token         *oauth2.Token
	credsPath     string
	syncToken     string
	syncTokenPath string
}

func generatePKCE() (verifier, challenge string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("failed to generate random bytes: %w", err)
	}
	verifier = base64.RawURLEncoding.EncodeToString(b)
	h := sha256.New()
	h.Write([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(h.Sum(nil))
	return verifier, challenge, nil
}

func NewGoogleContactsProvider(dir string) (*GoogleContactsProvider, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory: %w", err)
	}
	return &GoogleContactsProvider{
		credsPath:     filepath.Join(dir, "google_creds.json"),
		syncTokenPath: filepath.Join(dir, "google_sync_token.txt"),
	}, nil
}

func (g *GoogleContactsProvider) SaveCredentials(creds *GoogleCredentials) error {
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal credentials: %w", err)
	}
	if err := os.WriteFile(g.credsPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write credentials file: %w", err)
	}
	return nil
}

func (g *GoogleContactsProvider) LoadCredentials() (*GoogleCredentials, error) {
	data, err := os.ReadFile(g.credsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("credentials file not found at %s: please run init first", g.credsPath)
		}
		return nil, fmt.Errorf("failed to read credentials file: %w", err)
	}
	var creds GoogleCredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("failed to parse credentials file: %w", err)
	}
	return &creds, nil
}

func (g *GoogleContactsProvider) Initialize() error {
	creds, err := g.LoadCredentials()
	if err != nil {
		return err
	}
	g.config = &oauth2.Config{
		ClientID:     creds.ClientID,
		ClientSecret: creds.ClientSecret,
		Endpoint:     google.Endpoint,
		RedirectURL:  "http://localhost:8080/callback",
		Scopes: []string{
			"https://www.googleapis.com/auth/contacts",
			"https://www.googleapis.com/auth/userinfo.email",
		},
	}
	if creds.RefreshToken != "" {
		g.token = &oauth2.Token{
			RefreshToken: creds.RefreshToken,
			AccessToken:  creds.AccessToken,
			Expiry:       time.Now().Add(-time.Hour),
		}
	}
	if data, err := os.ReadFile(g.syncTokenPath); err == nil {
		g.syncToken = string(data)
	}
	return nil
}

func (g *GoogleContactsProvider) AuthorizeWithPKCE(ctx context.Context) (authURL string, errChan <-chan error, err error) {
	if g.config == nil {
		return "", nil, fmt.Errorf("provider not initialized")
	}
	verifier, challenge, err := generatePKCE()
	if err != nil {
		return "", nil, fmt.Errorf("failed to generate PKCE: %w", err)
	}
	stateBytes := make([]byte, 16)
	rand.Read(stateBytes)
	state := base64.RawURLEncoding.EncodeToString(stateBytes)

	authURL = g.config.AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		oauth2.ApprovalForce,
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)

	resultCh := make(chan error, 1)
	mux := http.NewServeMux()
	server := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		if errMsg := r.URL.Query().Get("error"); errMsg != "" {
			errDesc := r.URL.Query().Get("error_description")
			http.Error(w, "Authorization failed", http.StatusBadRequest)
			resultCh <- fmt.Errorf("authorization failed: %s - %s", errMsg, errDesc)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "No authorization code received", http.StatusBadRequest)
			resultCh <- fmt.Errorf("no authorization code in callback")
			return
		}
		returnedState := r.URL.Query().Get("state")
		if returnedState != state {
			http.Error(w, "Invalid state parameter", http.StatusBadRequest)
			resultCh <- fmt.Errorf("state mismatch: CSRF attack detected")
			return
		}
		token, err := g.config.Exchange(ctx, code,
			oauth2.SetAuthURLParam("code_verifier", verifier),
		)
		if err != nil {
			http.Error(w, "Token exchange failed", http.StatusInternalServerError)
			resultCh <- fmt.Errorf("failed to exchange code: %w", err)
			return
		}
		g.token = token
		creds, err := g.LoadCredentials()
		if err != nil {
			http.Error(w, "Failed to save credentials", http.StatusInternalServerError)
			resultCh <- fmt.Errorf("failed to load credentials: %w", err)
			return
		}
		creds.RefreshToken = token.RefreshToken
		creds.AccessToken = token.AccessToken
		if err := g.SaveCredentials(creds); err != nil {
			http.Error(w, "Failed to save credentials", http.StatusInternalServerError)
			resultCh <- fmt.Errorf("failed to save credentials: %w", err)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html><head><title>Authorization Successful</title>
<style>.logo-container{width:200px;height:200px;margin:0 auto 30px;}.logo-container svg{width:100%%;height:100%%;}</style>
</head><body style="font-family:sans-serif;text-align:center;padding:50px;">
<div class="logo-container">%s</div>
<h1 style="color:#4CAF50;">Authorization Successful!</h1>
<p>You can close this window and return to the terminal.</p>
</body></html>`, logoSVG)
		resultCh <- nil
		go func() {
			time.Sleep(100 * time.Millisecond)
			server.Shutdown(context.Background())
		}()
	})

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			resultCh <- fmt.Errorf("server error: %w", err)
		}
	}()
	go func() {
		<-ctx.Done()
		server.Shutdown(context.Background())
		select {
		case resultCh <- ctx.Err():
		default:
		}
	}()

	return authURL, resultCh, nil
}

func (g *GoogleContactsProvider) SaveSyncToken(token string) error {
	g.syncToken = token
	return os.WriteFile(g.syncTokenPath, []byte(token), 0600)
}

func (g *GoogleContactsProvider) GetSyncToken() string {
	return g.syncToken
}

// --- People API response structures ---

type peopleAPIPerson struct {
	ResourceName   string                   `json:"resourceName"`
	ETag           string                   `json:"etag"`
	Names          []peopleAPIName          `json:"names"`
	Nicknames      []peopleAPINickname      `json:"nicknames"`
	PhoneNumbers   []peopleAPIPhoneNumber   `json:"phoneNumbers"`
	EmailAddresses []peopleAPIEmailAddress  `json:"emailAddresses"`
	Addresses      []peopleAPIAddress       `json:"addresses"`
	Organizations  []peopleAPIOrganization  `json:"organizations"`
	Birthdays      []peopleAPIBirthday      `json:"birthdays"`
	Photos         []peopleAPIPhoto         `json:"photos"`
	Biographies    []peopleAPIBiography     `json:"biographies"`
	URLs           []peopleAPIURL           `json:"urls"`
	Events         []peopleAPIEvent         `json:"events"`
	Genders        []peopleAPIGender        `json:"genders"`
	ImClients      []peopleAPIImClient      `json:"imClients"`
	Relations      []peopleAPIRelation      `json:"relations"`
	CalendarURLs   []peopleAPICalendarURL   `json:"calendarUrls"`
	SipAddresses   []peopleAPISipAddress    `json:"sipAddresses"`
	Locales        []peopleAPILocale        `json:"locales"`
	Interests      []peopleAPIInterest      `json:"interests"`
	Skills         []peopleAPISkill         `json:"skills"`
	Occupations    []peopleAPIOccupation    `json:"occupations"`
	Locations      []peopleAPILocation      `json:"locations"`
	Memberships    []peopleAPIMembership    `json:"memberships"`
	UserDefined    []peopleAPIUserDefined   `json:"userDefined"`
	ClientData     []peopleAPIClientData    `json:"clientData"`
	ExternalIds    []peopleAPIExternalId    `json:"externalIds"`
	MiscKeywords   []peopleAPIMiscKeyword   `json:"miscKeywords"`
	CoverPhotos    []peopleAPICoverPhoto    `json:"coverPhotos"`
	AgeRanges      []peopleAPIAgeRange      `json:"ageRanges"`
	Metadata       *peopleAPIPersonMetadata `json:"metadata"`
}

type peopleAPIName struct {
	DisplayName          string `json:"displayName"`
	FamilyName           string `json:"familyName"`
	GivenName            string `json:"givenName"`
	MiddleName           string `json:"middleName"`
	HonorificPrefix      string `json:"honorificPrefix"`
	HonorificSuffix      string `json:"honorificSuffix"`
	DisplayNameLastFirst string `json:"displayNameLastFirst"`
}

type peopleAPINickname struct {
	Value string `json:"value"`
}

type peopleAPIPhoneNumber struct {
	Value string `json:"value"`
	Type  string `json:"type"`
}

type peopleAPIEmailAddress struct {
	Value string `json:"value"`
	Type  string `json:"type"`
}

type peopleAPIAddress struct {
	StreetAddress   string `json:"streetAddress"`
	ExtendedAddress string `json:"extendedAddress"`
	City            string `json:"city"`
	Region          string `json:"region"`
	PostalCode      string `json:"postalCode"`
	Country         string `json:"country"`
	PostOfficeBox   string `json:"poBox"`
	Type            string `json:"type"`
}

type peopleAPIOrganization struct {
	Name       string `json:"name"`
	Title      string `json:"title"`
	Department string `json:"department"`
}

type peopleAPIBirthday struct {
	Date struct {
		Year  int `json:"year"`
		Month int `json:"month"`
		Day   int `json:"day"`
	} `json:"date"`
}

type peopleAPIPhoto struct {
	URL string `json:"url"`
}

type peopleAPIBiography struct {
	Value string `json:"value"`
}

type peopleAPIURL struct {
	Value string `json:"value"`
	Type  string `json:"type"`
}

type peopleAPIEvent struct {
	Date struct {
		Year  int `json:"year"`
		Month int `json:"month"`
		Day   int `json:"day"`
	} `json:"date"`
	Type string `json:"type"`
}

type peopleAPIGender struct {
	Value string `json:"value"`
}

type peopleAPIImClient struct {
	Username string `json:"username"`
	Protocol string `json:"protocol"`
	Type     string `json:"type"`
}

type peopleAPIRelation struct {
	Person string `json:"person"`
	Type   string `json:"type"`
}

type peopleAPICalendarURL struct {
	URL  string `json:"url"`
	Type string `json:"type"`
}

type peopleAPISipAddress struct {
	Value string `json:"value"`
	Type  string `json:"type"`
}

type peopleAPILocale struct {
	Value string `json:"value"`
}

type peopleAPIInterest struct {
	Value string `json:"value"`
}

type peopleAPISkill struct {
	Value string `json:"value"`
}

type peopleAPIOccupation struct {
	Value string `json:"value"`
}

type peopleAPILocation struct {
	Value string `json:"value"`
	Type  string `json:"type"`
}

type peopleAPIMembership struct {
	ContactGroupMembership *struct {
		ContactGroupResourceName string `json:"contactGroupResourceName"`
	} `json:"contactGroupMembership"`
}

type peopleAPIUserDefined struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type peopleAPIClientData struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type peopleAPIExternalId struct {
	Value string `json:"value"`
	Type  string `json:"type"`
}

type peopleAPIMiscKeyword struct {
	Value string `json:"value"`
	Type  string `json:"type"`
}

type peopleAPICoverPhoto struct {
	URL string `json:"url"`
}

type peopleAPIAgeRange struct {
	AgeRange string `json:"ageRange"`
}

type peopleAPIPersonMetadata struct {
	Sources []struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	} `json:"sources"`
}

// --- Conversion: People API → vcard.Card ---

func convertPeopleAPIToCard(person peopleAPIPerson) vcard.Card {
	card := make(vcard.Card)
	card.SetValue(vcard.FieldVersion, "4.0")

	// UID from resourceName
	uid := person.ResourceName
	if strings.Contains(uid, "/") {
		parts := strings.Split(uid, "/")
		uid = parts[len(parts)-1]
	}
	card.SetValue(vcard.FieldUID, uid)

	// ETag
	if person.ETag != "" {
		card.SetValue("X-GOOGLE-ETAG", person.ETag)
	}

	// Names → FN, N
	if len(person.Names) > 0 {
		name := person.Names[0]
		if name.DisplayName != "" {
			card.SetValue(vcard.FieldFormattedName, name.DisplayName)
		}
		// N: family;given;middle;prefix;suffix
		nField := &vcard.Field{
			Value: name.FamilyName + ";" + name.GivenName + ";" + name.MiddleName + ";" + name.HonorificPrefix + ";" + name.HonorificSuffix,
		}
		card[vcard.FieldName] = []*vcard.Field{nField}
	}

	// Nicknames → NICKNAME
	for _, nick := range person.Nicknames {
		card.Add(vcard.FieldNickname, &vcard.Field{Value: nick.Value})
	}

	// PhoneNumbers → TEL
	for _, phone := range person.PhoneNumbers {
		f := &vcard.Field{
			Value:  phone.Value,
			Params: vcard.Params{},
		}
		if phone.Type != "" {
			f.Params[vcard.ParamType] = []string{strings.ToLower(phone.Type)}
		}
		card.Add(vcard.FieldTelephone, f)
	}

	// EmailAddresses → EMAIL
	for _, email := range person.EmailAddresses {
		f := &vcard.Field{
			Value:  email.Value,
			Params: vcard.Params{},
		}
		if email.Type != "" {
			f.Params[vcard.ParamType] = []string{strings.ToLower(email.Type)}
		}
		card.Add(vcard.FieldEmail, f)
	}

	// Addresses → ADR
	for _, addr := range person.Addresses {
		// ADR: PO Box;Extended;Street;City;Region;PostalCode;Country
		adrValue := addr.PostOfficeBox + ";" + addr.ExtendedAddress + ";" + addr.StreetAddress + ";" + addr.City + ";" + addr.Region + ";" + addr.PostalCode + ";" + addr.Country
		f := &vcard.Field{
			Value:  adrValue,
			Params: vcard.Params{},
		}
		if addr.Type != "" {
			f.Params[vcard.ParamType] = []string{strings.ToLower(addr.Type)}
		}
		card.Add(vcard.FieldAddress, f)
	}

	// Organizations → ORG, TITLE
	if len(person.Organizations) > 0 {
		org := person.Organizations[0]
		orgParts := org.Name
		if org.Department != "" {
			orgParts += ";" + org.Department
		}
		card.SetValue(vcard.FieldOrganization, orgParts)
		if org.Title != "" {
			card.SetValue(vcard.FieldTitle, org.Title)
		}
	}

	// Birthdays → BDAY
	if len(person.Birthdays) > 0 {
		bday := person.Birthdays[0]
		if bday.Date.Year > 0 && bday.Date.Month > 0 && bday.Date.Day > 0 {
			card.SetValue(vcard.FieldBirthday, fmt.Sprintf("%04d%02d%02d", bday.Date.Year, bday.Date.Month, bday.Date.Day))
		} else if bday.Date.Month > 0 && bday.Date.Day > 0 {
			card.SetValue(vcard.FieldBirthday, fmt.Sprintf("--%02d%02d", bday.Date.Month, bday.Date.Day))
		}
	}

	// Photos → PHOTO
	for _, photo := range person.Photos {
		card.Add(vcard.FieldPhoto, &vcard.Field{Value: photo.URL})
	}

	// Biographies → NOTE
	for _, bio := range person.Biographies {
		card.Add(vcard.FieldNote, &vcard.Field{Value: bio.Value})
	}

	// URLs → URL
	for _, u := range person.URLs {
		f := &vcard.Field{
			Value:  u.Value,
			Params: vcard.Params{},
		}
		if u.Type != "" {
			f.Params[vcard.ParamType] = []string{strings.ToLower(u.Type)}
		}
		card.Add(vcard.FieldURL, f)
	}

	// Events → ANNIVERSARY or X-GOOGLE-EVENT
	for _, event := range person.Events {
		dateStr := ""
		if event.Date.Year > 0 {
			dateStr = fmt.Sprintf("%04d%02d%02d", event.Date.Year, event.Date.Month, event.Date.Day)
		} else if event.Date.Month > 0 && event.Date.Day > 0 {
			dateStr = fmt.Sprintf("--%02d%02d", event.Date.Month, event.Date.Day)
		}
		if dateStr == "" {
			continue
		}
		if strings.ToLower(event.Type) == "anniversary" {
			card.SetValue(vcard.FieldAnniversary, dateStr)
		} else {
			f := &vcard.Field{
				Value:  dateStr,
				Params: vcard.Params{},
			}
			if event.Type != "" {
				f.Params[vcard.ParamType] = []string{event.Type}
			}
			card.Add("X-GOOGLE-EVENT", f)
		}
	}

	// Genders → GENDER
	for _, gender := range person.Genders {
		card.SetValue(vcard.FieldGender, gender.Value)
	}

	// ImClients → IMPP
	for _, im := range person.ImClients {
		protocol := strings.ToLower(im.Protocol)
		uri := protocol + ":" + im.Username
		f := &vcard.Field{
			Value:  uri,
			Params: vcard.Params{},
		}
		if im.Type != "" {
			f.Params[vcard.ParamType] = []string{strings.ToLower(im.Type)}
		}
		card.Add(vcard.FieldIMPP, f)
	}

	// Relations → RELATED
	for _, rel := range person.Relations {
		f := &vcard.Field{
			Value:  rel.Person,
			Params: vcard.Params{},
		}
		if rel.Type != "" {
			f.Params[vcard.ParamType] = []string{strings.ToLower(rel.Type)}
		}
		card.Add(vcard.FieldRelated, f)
	}

	// CalendarURLs → CALURI
	for _, cal := range person.CalendarURLs {
		f := &vcard.Field{
			Value:  cal.URL,
			Params: vcard.Params{},
		}
		if cal.Type != "" {
			f.Params[vcard.ParamType] = []string{strings.ToLower(cal.Type)}
		}
		card.Add(vcard.FieldCalendarURI, f)
	}

	// SipAddresses → IMPP with sip: prefix
	for _, sip := range person.SipAddresses {
		f := &vcard.Field{
			Value:  "sip:" + sip.Value,
			Params: vcard.Params{},
		}
		if sip.Type != "" {
			f.Params[vcard.ParamType] = []string{strings.ToLower(sip.Type)}
		}
		card.Add(vcard.FieldIMPP, f)
	}

	// Locales → LANG
	for _, locale := range person.Locales {
		card.Add(vcard.FieldLanguage, &vcard.Field{Value: locale.Value})
	}

	// --- X-GOOGLE-* extensions ---

	// Interests
	for _, interest := range person.Interests {
		card.Add("X-GOOGLE-INTEREST", &vcard.Field{Value: interest.Value})
	}

	// Skills
	for _, skill := range person.Skills {
		card.Add("X-GOOGLE-SKILL", &vcard.Field{Value: skill.Value})
	}

	// Occupations
	for _, occ := range person.Occupations {
		card.Add("X-GOOGLE-OCCUPATION", &vcard.Field{Value: occ.Value})
	}

	// Locations
	for _, loc := range person.Locations {
		f := &vcard.Field{
			Value:  loc.Value,
			Params: vcard.Params{},
		}
		if loc.Type != "" {
			f.Params[vcard.ParamType] = []string{loc.Type}
		}
		card.Add("X-GOOGLE-LOCATION", f)
	}

	// Memberships
	for _, mem := range person.Memberships {
		if mem.ContactGroupMembership != nil {
			card.Add("X-GOOGLE-GROUP-MEMBERSHIP", &vcard.Field{Value: mem.ContactGroupMembership.ContactGroupResourceName})
		}
	}

	// UserDefined
	for _, ud := range person.UserDefined {
		card.Add("X-GOOGLE-CUSTOM-"+strings.ToUpper(strings.ReplaceAll(ud.Key, " ", "-")), &vcard.Field{Value: ud.Value})
	}

	// ClientData
	for _, cd := range person.ClientData {
		card.Add("X-GOOGLE-CLIENT-"+strings.ToUpper(strings.ReplaceAll(cd.Key, " ", "-")), &vcard.Field{Value: cd.Value})
	}

	// ExternalIds
	for _, eid := range person.ExternalIds {
		f := &vcard.Field{
			Value:  eid.Value,
			Params: vcard.Params{},
		}
		if eid.Type != "" {
			f.Params[vcard.ParamType] = []string{eid.Type}
		}
		card.Add("X-GOOGLE-EXTERNAL-ID", f)
	}

	// MiscKeywords
	for _, kw := range person.MiscKeywords {
		f := &vcard.Field{
			Value:  kw.Value,
			Params: vcard.Params{},
		}
		if kw.Type != "" {
			f.Params[vcard.ParamType] = []string{kw.Type}
		}
		card.Add("X-GOOGLE-KEYWORD", f)
	}

	// CoverPhotos
	for _, cp := range person.CoverPhotos {
		card.Add("X-GOOGLE-COVER-PHOTO", &vcard.Field{Value: cp.URL})
	}

	// AgeRanges
	for _, ar := range person.AgeRanges {
		card.Add("X-GOOGLE-AGE-RANGE", &vcard.Field{Value: ar.AgeRange})
	}

	// Metadata sources
	if person.Metadata != nil {
		for _, src := range person.Metadata.Sources {
			card.Add("X-GOOGLE-SOURCE", &vcard.Field{
				Value:  src.ID,
				Params: vcard.Params{vcard.ParamType: []string{src.Type}},
			})
		}
	}

	// Ensure FN is set (vCard requires it)
	if CardFullName(card) == "" {
		card.SetValue(vcard.FieldFormattedName, uid)
	}

	return card
}

// --- Conversion: vcard.Card → People API ---

func convertCardToPeopleAPI(card vcard.Card) map[string]interface{} {
	person := make(map[string]interface{})

	// N → names
	fn := CardFullName(card)
	nFields := card[vcard.FieldName]
	if len(nFields) > 0 {
		parts := strings.SplitN(nFields[0].Value, ";", 5)
		nameMap := map[string]interface{}{}
		if len(parts) > 0 {
			nameMap["familyName"] = parts[0]
		}
		if len(parts) > 1 {
			nameMap["givenName"] = parts[1]
		}
		if len(parts) > 2 {
			nameMap["middleName"] = parts[2]
		}
		if len(parts) > 3 {
			nameMap["honorificPrefix"] = parts[3]
		}
		if len(parts) > 4 {
			nameMap["honorificSuffix"] = parts[4]
		}
		person["names"] = []map[string]interface{}{nameMap}
	} else if fn != "" {
		person["names"] = []map[string]interface{}{{"displayName": fn}}
	}

	// TEL → phoneNumbers
	if tels := card[vcard.FieldTelephone]; len(tels) > 0 {
		phones := make([]map[string]interface{}, len(tels))
		for i, f := range tels {
			phones[i] = map[string]interface{}{"value": f.Value, "type": f.Params.Get(vcard.ParamType)}
		}
		person["phoneNumbers"] = phones
	}

	// EMAIL → emailAddresses
	if emails := card[vcard.FieldEmail]; len(emails) > 0 {
		addrs := make([]map[string]interface{}, len(emails))
		for i, f := range emails {
			addrs[i] = map[string]interface{}{"value": f.Value, "type": f.Params.Get(vcard.ParamType)}
		}
		person["emailAddresses"] = addrs
	}

	// ADR → addresses
	if adrs := card[vcard.FieldAddress]; len(adrs) > 0 {
		addresses := make([]map[string]interface{}, len(adrs))
		for i, f := range adrs {
			parts := strings.SplitN(f.Value, ";", 7)
			m := map[string]interface{}{"type": f.Params.Get(vcard.ParamType)}
			if len(parts) > 0 {
				m["poBox"] = parts[0]
			}
			if len(parts) > 1 {
				m["extendedAddress"] = parts[1]
			}
			if len(parts) > 2 {
				m["streetAddress"] = parts[2]
			}
			if len(parts) > 3 {
				m["city"] = parts[3]
			}
			if len(parts) > 4 {
				m["region"] = parts[4]
			}
			if len(parts) > 5 {
				m["postalCode"] = parts[5]
			}
			if len(parts) > 6 {
				m["country"] = parts[6]
			}
			addresses[i] = m
		}
		person["addresses"] = addresses
	}

	// ORG, TITLE → organizations
	orgVal := card.Value(vcard.FieldOrganization)
	titleVal := card.Value(vcard.FieldTitle)
	if orgVal != "" || titleVal != "" {
		orgMap := map[string]interface{}{}
		if orgVal != "" {
			parts := strings.SplitN(orgVal, ";", 2)
			orgMap["name"] = parts[0]
			if len(parts) > 1 {
				orgMap["department"] = parts[1]
			}
		}
		if titleVal != "" {
			orgMap["title"] = titleVal
		}
		person["organizations"] = []map[string]interface{}{orgMap}
	}

	// BDAY → birthdays
	if bday := card.Value(vcard.FieldBirthday); bday != "" {
		dateMap := parseDateValue(bday)
		if dateMap != nil {
			person["birthdays"] = []map[string]interface{}{{"date": dateMap}}
		}
	}

	// NOTE → biographies
	if notes := card[vcard.FieldNote]; len(notes) > 0 {
		bios := make([]map[string]interface{}, len(notes))
		for i, f := range notes {
			bios[i] = map[string]interface{}{"value": f.Value}
		}
		person["biographies"] = bios
	}

	// URL → urls
	if urls := card[vcard.FieldURL]; len(urls) > 0 {
		us := make([]map[string]interface{}, len(urls))
		for i, f := range urls {
			us[i] = map[string]interface{}{"value": f.Value, "type": f.Params.Get(vcard.ParamType)}
		}
		person["urls"] = us
	}

	return person
}

// parseDateValue parses YYYYMMDD or --MMDD vCard date format.
func parseDateValue(s string) map[string]int {
	s = strings.ReplaceAll(s, "-", "")
	if len(s) == 8 {
		// YYYYMMDD
		t, err := time.Parse("20060102", s)
		if err != nil {
			return nil
		}
		return map[string]int{"year": t.Year(), "month": int(t.Month()), "day": t.Day()}
	}
	if len(s) == 4 {
		// MMDD (after stripping --)
		t, err := time.Parse("0102", s)
		if err != nil {
			return nil
		}
		return map[string]int{"year": 0, "month": int(t.Month()), "day": t.Day()}
	}
	return nil
}

// --- Provider methods ---

func (g *GoogleContactsProvider) FetchContacts() ([]vcard.Card, error) {
	ctx := context.Background()
	if g.config == nil || g.token == nil {
		return nil, fmt.Errorf("provider not initialized or not authenticated")
	}
	httpClient := g.config.Client(ctx, g.token)
	newToken, err := g.config.TokenSource(ctx, g.token).Token()
	if err != nil {
		return nil, fmt.Errorf("failed to refresh token: %w", err)
	}
	g.token = newToken
	httpClient = g.config.Client(ctx, g.token)

	creds, err := g.LoadCredentials()
	if err != nil {
		return nil, fmt.Errorf("failed to load credentials: %w", err)
	}
	creds.RefreshToken = newToken.RefreshToken
	creds.AccessToken = newToken.AccessToken
	if err := g.SaveCredentials(creds); err != nil {
		return nil, fmt.Errorf("failed to save refreshed token: %w", err)
	}

	var allCards []vcard.Card
	pageToken := ""
	for {
		params := url.Values{
			"personFields": []string{allPersonFields},
			"pageSize":     []string{"1000"},
			"sources":      []string{"READ_SOURCE_TYPE_CONTACT"},
		}
		if pageToken != "" {
			params.Set("pageToken", pageToken)
		}
		apiURL := "https://people.googleapis.com/v1/people/me/connections?" + params.Encode()
		resp, err := httpClient.Get(apiURL)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch contacts: %w", err)
		}
		defer resp.Body.Close()
		bodyBytes, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("People API request failed with status %d: %s", resp.StatusCode, string(bodyBytes))
		}
		var result struct {
			Connections   []peopleAPIPerson `json:"connections"`
			NextPageToken string            `json:"nextPageToken"`
		}
		if err := json.Unmarshal(bodyBytes, &result); err != nil {
			return nil, fmt.Errorf("failed to decode People API response: %w", err)
		}
		for _, person := range result.Connections {
			card := convertPeopleAPIToCard(person)
			allCards = append(allCards, card)
		}
		if result.NextPageToken == "" {
			break
		}
		pageToken = result.NextPageToken
	}
	return allCards, nil
}

func (g *GoogleContactsProvider) WriteContact(card vcard.Card) error {
	ctx := context.Background()
	if g.config == nil || g.token == nil {
		return fmt.Errorf("provider not initialized or not authenticated")
	}
	httpClient := g.config.Client(ctx, g.token)
	personData := convertCardToPeopleAPI(card)
	var req *http.Request
	var apiURL string
	var err error

	uid := CardUID(card)
	isExistingGoogleContact := !strings.Contains(uid, "-")
	if isExistingGoogleContact {
		resourceName := fmt.Sprintf("people/%s", uid)
		apiURL = fmt.Sprintf("https://people.googleapis.com/v1/%s:updateContact", resourceName)
		params := url.Values{}
		params.Set("updatePersonFields", "names,phoneNumbers,emailAddresses,addresses,organizations,birthdays,biographies,urls")
		apiURL += "?" + params.Encode()

		// Include etag for update
		if etag := card.Value("X-GOOGLE-ETAG"); etag != "" {
			personData["etag"] = etag
		}
		body, _ := json.Marshal(personData)
		req, err = http.NewRequest("PATCH", apiURL, strings.NewReader(string(body)))
	} else {
		apiURL = "https://people.googleapis.com/v1/people:createContact"
		body, _ := json.Marshal(personData)
		req, err = http.NewRequest("POST", apiURL, strings.NewReader(string(body)))
	}
	if err != nil {
		return fmt.Errorf("failed to create request for contact %s: %w", CardFullName(card), err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to update contact %s: %w", CardFullName(card), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to update contact %s (status %d): %s", CardFullName(card), resp.StatusCode, string(body))
	}
	return nil
}

func (g *GoogleContactsProvider) DeleteContact(uid string) error {
	ctx := context.Background()
	if g.config == nil || g.token == nil {
		return fmt.Errorf("provider not initialized or not authenticated")
	}
	httpClient := g.config.Client(ctx, g.token)
	resourceName := fmt.Sprintf("people/%s", uid)
	apiURL := fmt.Sprintf("https://people.googleapis.com/v1/%s:deleteContact", resourceName)
	req, err := http.NewRequest("DELETE", apiURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create delete request for contact %s: %w", uid, err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to delete contact %s: %w", uid, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to delete contact %s (status %d): %s", uid, resp.StatusCode, string(body))
	}
	return nil
}
