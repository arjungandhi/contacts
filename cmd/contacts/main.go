package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/arjungandhi/contacts"
	"github.com/charmbracelet/huh"
	"github.com/emersion/go-vcard"
	"github.com/rwxrob/bonzai"
	Z "github.com/rwxrob/bonzai/z"
	"github.com/rwxrob/help"
)

// contactCompleter completes contact names for commands that take a contact arg.
type contactCompleter struct{}

func (contactCompleter) Complete(_ bonzai.Command, args ...string) []string {
	cm, err := getManagerQuiet()
	if err != nil {
		return []string{}
	}
	cards, err := cm.ListContacts()
	if err != nil {
		return []string{}
	}
	prefix := ""
	if len(args) > 0 {
		prefix = strings.ToLower(args[0])
	}
	var matches []string
	for _, card := range cards {
		name := contacts.CardFullName(card)
		if name == "" {
			continue
		}
		if prefix == "" || strings.HasPrefix(strings.ToLower(name), prefix) {
			matches = append(matches, name)
		}
	}
	return matches
}

var Cmd = &Z.Cmd{
	Name:     "contacts",
	Summary:  "manage your contacts",
	Commands: []*Z.Cmd{help.Cmd, initCmd, syncCmd, listCmd, getCmd, deleteCmd},
}

var initCmd = &Z.Cmd{
	Name:    "init",
	Summary: "initialize google contacts provider",
	Call: func(x *Z.Cmd, args ...string) error {
		cfg := contacts.NewConfig()
		if err := cfg.EnsureDir(); err != nil {
			return err
		}

		provider, _ := contacts.NewGoogleContactsProvider(cfg.Dir)
		existingCreds, _ := provider.LoadCredentials()

		if existingCreds != nil && existingCreds.ClientID != "" {
			var reauth bool
			form := huh.NewForm(huh.NewGroup(
				huh.NewConfirm().
					Title("Existing credentials found").
					Description(fmt.Sprintf("Client ID: %s\nDelete and enter new credentials?", existingCreds.ClientID)).
					Affirmative("Yes, delete").
					Negative("No, re-authorize").
					Value(&reauth),
			))
			if err := form.Run(); err != nil {
				return err
			}
			if !reauth {
				return authorize(cfg, provider)
			}
		}

		var clientID, clientSecret string
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewNote().
					Title("Google Contacts Setup").
					Description("Steps:\n1. Enable People API at console.cloud.google.com/apis/library/people.googleapis.com\n2. Go to console.cloud.google.com/apis/credentials\n3. Create OAuth 2.0 Client ID (Desktop app)\n4. Add redirect URI: http://localhost:8080/callback"),
			),
			huh.NewGroup(
				huh.NewInput().Title("Client ID").Value(&clientID).
					Validate(func(s string) error {
						if strings.TrimSpace(s) == "" {
							return fmt.Errorf("required")
						}
						return nil
					}),
				huh.NewInput().Title("Client Secret").Value(&clientSecret).Password(true).
					Validate(func(s string) error {
						if strings.TrimSpace(s) == "" {
							return fmt.Errorf("required")
						}
						return nil
					}),
			),
		)
		if err := form.Run(); err != nil {
			return err
		}

		provider, err := contacts.NewGoogleContactsProvider(cfg.Dir)
		if err != nil {
			return err
		}
		creds := &contacts.GoogleCredentials{
			ClientID:     strings.TrimSpace(clientID),
			ClientSecret: strings.TrimSpace(clientSecret),
		}
		if err := provider.SaveCredentials(creds); err != nil {
			return err
		}
		if err := provider.Initialize(); err != nil {
			return err
		}
		return authorize(cfg, provider)
	},
}

func authorize(cfg *contacts.Config, provider *contacts.GoogleContactsProvider) error {
	if err := provider.Initialize(); err != nil {
		return err
	}
	ctx := context.Background()
	authURL, errChan, err := provider.AuthorizeWithPKCE(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Opening browser for authorization...\nIf it doesn't open, visit:\n\n  %s\n\nWaiting for authorization...\n", authURL)
	_ = openBrowser(authURL)
	if err := <-errChan; err != nil {
		return fmt.Errorf("authorization failed: %w", err)
	}
	fmt.Fprintln(os.Stderr, "Google Contacts initialized. Run 'contacts sync' to sync.")
	return nil
}

var syncCmd = &Z.Cmd{
	Name:    "sync",
	Summary: "sync contacts from google",
	Call: func(x *Z.Cmd, args ...string) error {
		cm, err := getManager()
		if err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "Syncing contacts...")
		if err := cm.SyncContacts(); err != nil {
			return err
		}
		list, err := cm.ListContacts()
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Sync complete. %d contacts.\n", len(list))
		return nil
	},
}

var listCmd = &Z.Cmd{
	Name:    "list",
	Summary: "list all contacts (pipe-friendly)",
	Call: func(x *Z.Cmd, args ...string) error {
		cm, err := getManager()
		if err != nil {
			return err
		}
		list, err := cm.ListContacts()
		if err != nil {
			return err
		}
		for _, card := range list {
			fmt.Printf("%s|%s|%s|%s\n",
				contacts.CardUID(card),
				contacts.CardFullName(card),
				contacts.PrimaryEmail(card),
				contacts.PrimaryPhone(card),
			)
		}
		return nil
	},
}

var getCmd = &Z.Cmd{
	Name:    "get",
	Summary: "get a contact by name or UID (--vcf for raw vCard)",
	Usage:   "[--vcf] <name|uid>",
	MinArgs: 1,
	Comp:    contactCompleter{},
	Call: func(x *Z.Cmd, args ...string) error {
		vcf := false
		parts := args
		if args[0] == "--vcf" {
			vcf = true
			parts = args[1:]
		}
		if len(parts) == 0 {
			return fmt.Errorf("missing argument")
		}
		query := strings.Join(parts, " ")
		cm, err := getManager()
		if err != nil {
			return err
		}
		card, err := cm.ResolveContact(query)
		if err != nil {
			return err
		}
		if card == nil {
			return fmt.Errorf("contact not found: %s", query)
		}
		if vcf {
			data, err := contacts.EncodeCard(card)
			if err != nil {
				return err
			}
			fmt.Print(string(data))
		} else {
			if supportsKittyGraphics() {
				renderPhoto(card)
			}
			fmt.Println(contacts.FormatCard(card))
		}
		return nil
	},
}

var deleteCmd = &Z.Cmd{
	Name:    "delete",
	Summary: "delete a contact by name or UID",
	Usage:   "<name|uid>",
	MinArgs: 1,
	Comp:    contactCompleter{},
	Call: func(x *Z.Cmd, args ...string) error {
		query := strings.Join(args, " ")
		cm, err := getManager()
		if err != nil {
			return err
		}
		card, err := cm.ResolveContact(query)
		if err != nil {
			return err
		}
		if card == nil {
			return fmt.Errorf("contact not found: %s", query)
		}
		uid := contacts.CardUID(card)
		fmt.Fprintf(os.Stderr, "Delete %q? [y/N] ", contacts.CardFullName(card))
		var response string
		fmt.Scanln(&response)
		if strings.ToLower(response) != "y" {
			fmt.Fprintln(os.Stderr, "Cancelled.")
			return nil
		}
		if err := cm.DeleteContact(uid); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "Deleted.")
		return nil
	},
}

func getManager() (*contacts.ContactManager, error) {
	cfg := contacts.NewConfig()
	if err := cfg.EnsureDir(); err != nil {
		return nil, err
	}
	provider, err := contacts.NewGoogleContactsProvider(cfg.Dir)
	if err != nil {
		return nil, err
	}
	if err := provider.Initialize(); err != nil {
		return nil, fmt.Errorf("%w. Run 'contacts init' first", err)
	}
	return contacts.NewContactManager(provider, cfg.Dir)
}

// getManagerQuiet returns a manager without provider init (for completion).
func getManagerQuiet() (*contacts.ContactManager, error) {
	cfg := contacts.NewConfig()
	return contacts.NewContactManager(nil, cfg.Dir)
}

// supportsKittyGraphics sends a graphics protocol query action followed by a
// device attributes request. If the terminal understands the protocol it replies
// to the graphics query; otherwise only the device attributes response arrives.
func supportsKittyGraphics() bool {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return false
	}

	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return false
	}
	defer term.Restore(fd, oldState)

	// Query: 1x1 pixel, 24-bit, query action, direct transmission + device attributes request
	os.Stdout.WriteString("\033_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\033\\\033[c")

	// Read response with timeout
	buf := make([]byte, 256)
	deadline := time.Now().Add(500 * time.Millisecond)
	var response []byte
	for time.Now().Before(deadline) {
		os.Stdin.SetReadDeadline(deadline)
		n, err := os.Stdin.Read(buf)
		if n > 0 {
			response = append(response, buf[:n]...)
			// Device attributes response ends with 'c'
			if bytes.ContainsRune(response, 'c') {
				break
			}
		}
		if err != nil {
			break
		}
	}
	os.Stdin.SetReadDeadline(time.Time{})

	// If the response contains _G, the terminal answered the graphics query
	return bytes.Contains(response, []byte("_G"))
}

// renderPhoto fetches the contact's photo URL and displays it inline
// using the Kitty graphics protocol (supported by Ghostty, Kitty, etc.).
func renderPhoto(card vcard.Card) {
	photos := card[vcard.FieldPhoto]
	if len(photos) == 0 || photos[0].Value == "" {
		return
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(photos[0].Value)
	if err != nil || resp.StatusCode != http.StatusOK {
		return
	}
	defer resp.Body.Close()

	img, _, err := image.Decode(resp.Body)
	if err != nil {
		return
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return
	}

	b64 := base64.StdEncoding.EncodeToString(buf.Bytes())

	const chunkSize = 4096
	for i := 0; i < len(b64); i += chunkSize {
		end := i + chunkSize
		if end > len(b64) {
			end = len(b64)
		}
		chunk := b64[i:end]

		if i == 0 {
			// First chunk: set action=transmit+display, format=PNG, display height=8 rows
			m := 0
			if end < len(b64) {
				m = 1
			}
			fmt.Fprintf(os.Stdout, "\033_Ga=T,f=100,r=8,m=%d;%s\033\\", m, chunk)
		} else if end >= len(b64) {
			fmt.Fprintf(os.Stdout, "\033_Gm=0;%s\033\\", chunk)
		} else {
			fmt.Fprintf(os.Stdout, "\033_Gm=1;%s\033\\", chunk)
		}
	}
	fmt.Println()
}

func openBrowser(url string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "linux":
		cmd = "xdg-open"
		args = []string{url}
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	default:
		return fmt.Errorf("unsupported platform")
	}
	return exec.Command(cmd, args...).Start()
}

func main() {
	Cmd.Run()
}
