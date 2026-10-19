package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"

	"foundry/internal/domain"
)

var ErrUnknownCommand = errors.New("unknown catalog command")

type Command struct {
	ExpectedRevision int64           `json:"expected_revision"`
	Kind             string          `json:"kind"`
	Input            json.RawMessage `json:"input"`
}

type projectCreateInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}
type projectUpdateInput struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}
type targetCreateInput struct {
	ProjectID    string `json:"project_id"`
	Name         string `json:"name"`
	Platform     string `json:"platform"`
	Format       string `json:"format"`
	Architecture string `json:"architecture"`
	Distribution string `json:"distribution"`
}
type renameInput struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type environmentCreateInput struct {
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
	Tier      string `json:"tier"`
}
type resourceCreateInput struct {
	Name           string  `json:"name"`
	Kind           string  `json:"kind"`
	OwnerProjectID *string `json:"owner_project_id"`
	Provider       string  `json:"provider"`
	Account        string  `json:"account"`
	Region         string  `json:"region"`
	ExternalID     string  `json:"external_id"`
}
type resourceBindInput struct {
	ProjectID     string `json:"project_id"`
	EnvironmentID string `json:"environment_id"`
	ResourceID    string `json:"resource_id"`
	Purpose       string `json:"purpose"`
}
type resourceUnbindInput struct {
	ID string `json:"id"`
}
type missionLinkInput struct {
	MissionID string `json:"mission_id"`
	ProjectID string `json:"project_id"`
}
type missionRelinkInput struct {
	MissionID     string `json:"mission_id"`
	FromProjectID string `json:"from_project_id"`
	ToProjectID   string `json:"to_project_id"`
}

func invalidInput() error {
	return fmt.Errorf("%w: command fields are invalid", domain.ErrInvalidCatalog)
}

func DecodeCommand(raw []byte) (Command, error) {
	var command Command
	if err := decodeInput(raw, &command); err != nil {
		return Command{}, err
	}
	if command.ExpectedRevision < 0 || command.Kind == "" {
		return Command{}, invalidInput()
	}
	input := bytes.TrimSpace(command.Input)
	if len(input) == 0 || input[0] != '{' {
		return Command{}, invalidInput()
	}
	return command, nil
}

// Field presence and exact spelling are checked before the struct decoder:
// encoding/json alone permits missing fields, null strings and case aliases.
func decodeInput(raw []byte, out any) error {
	if len(raw) > 1048576 || !utf8.Valid(raw) {
		return invalidInput()
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := scanJSON(dec, 0); err != nil {
		return invalidInput()
	}
	if _, err := dec.Token(); err != io.EOF {
		return invalidInput()
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return invalidInput()
	}
	value := reflect.ValueOf(out).Elem()
	typ := value.Type()
	if len(fields) != typ.NumField() {
		return invalidInput()
	}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		key := field.Tag.Get("json")
		encoded, ok := fields[key]
		if !ok {
			return invalidInput()
		}
		if bytes.Equal(bytes.TrimSpace(encoded), []byte("null")) && key != "owner_project_id" {
			return invalidInput()
		}
	}
	dec = json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return invalidInput()
	}
	for i := 0; i < typ.NumField(); i++ {
		key := typ.Field(i).Tag.Get("json")
		if key == "id" || strings.HasSuffix(key, "_id") && key != "external_id" {
			field := value.Field(i)
			if field.Kind() == reflect.Pointer {
				if field.IsNil() {
					continue
				}
				field = field.Elem()
			}
			if !validID(field.String()) {
				return invalidInput()
			}
		}
		if key == "name" {
			name := value.Field(i).String()
			for _, r := range name {
				if unicode.IsControl(r) {
					return invalidInput()
				}
			}
			value.Field(i).SetString(strings.TrimSpace(name))
		}
	}
	return nil
}

func validID(id string) bool {
	if len(id) < 1 || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func scanJSON(dec *json.Decoder, depth int) error {
	if depth > 100 {
		return invalidInput()
	}
	token, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		keys := map[string]bool{}
		for dec.More() {
			token, err := dec.Token()
			if err != nil {
				return err
			}
			key, ok := token.(string)
			if !ok || keys[key] {
				return invalidInput()
			}
			keys[key] = true
			if err = scanJSON(dec, depth+1); err != nil {
				return err
			}
		}
		end, err := dec.Token()
		if err != nil || end != json.Delim('}') {
			return invalidInput()
		}
	case '[':
		for dec.More() {
			if err = scanJSON(dec, depth+1); err != nil {
				return err
			}
		}
		end, err := dec.Token()
		if err != nil || end != json.Delim(']') {
			return invalidInput()
		}
	default:
		return invalidInput()
	}
	return nil
}
