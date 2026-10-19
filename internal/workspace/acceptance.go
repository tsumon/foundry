package workspace

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
)

// AcceptanceInputs implements a deliberately bounded grammar, not a general shell
// parser. Repository scripts must use that same grammar, so mutable helpers cannot
// hide behind an otherwise frozen entry point. Image-owned verifiers are trusted.
func AcceptanceInputs(base string, commands []string) ([]string, error) {
	files, err := readTree(base)
	if err != nil {
		return nil, err
	}
	frozen, visiting := map[string]bool{}, map[string]bool{}
	var check func(string, int) error
	check = func(command string, depth int) error {
		words, err := acceptanceWords(command)
		if err != nil || len(words) == 0 {
			return errors.New("unsupported acceptance syntax")
		}
		if imageVerifier(words[0]) {
			return nil
		}
		switch words[0] {
		case "grep":
			i := 1
			for i < len(words) && strings.HasPrefix(words[i], "-") {
				option := words[i]
				i++
				if option == "--" {
					break
				}
				if len(option) < 2 {
					return errors.New("unsupported grep option")
				}
				for _, flag := range option[1:] {
					if !strings.ContainsRune("FqnxiE", flag) {
						return errors.New("unsupported grep option")
					}
				}
			}
			if len(words)-i != 2 {
				return errors.New("grep requires one literal pattern and one subject path")
			}
			_, err := acceptanceRepoPath(words[i+1])
			return err
		case "test":
			if len(words) != 3 || (words[1] != "-f" && words[1] != "-s" && words[1] != "-d") {
				return errors.New("test supports only -f, -s, or -d with one subject path")
			}
			_, err := acceptanceRepoPath(words[2])
			return err
		case "set":
			if depth > 0 && len(words) == 2 && words[1] == "-eu" {
				return nil
			}
		case "exit":
			if depth > 0 && len(words) == 2 && (words[1] == "0" || words[1] == "1") {
				return nil
			}
		case "sh":
			if len(words) >= 2 && imageVerifier(words[1]) {
				return nil
			}
			if len(words) != 2 {
				return errors.New("repository sh requires one script path and no arguments")
			}
			p, err := acceptanceRepoPath(words[1])
			if err != nil {
				return err
			}
			if depth >= 16 || visiting[p] {
				return errors.New("acceptance script recursion exceeds bound or cycles")
			}
			if frozen[p] {
				return nil
			}
			// Exact tree spelling avoids case-insensitive host aliases bypassing
			// the protected-path rule used by planning and edit application.
			b, exists := files[p]
			if !exists {
				return fmt.Errorf("acceptance script must match a committed regular file exactly: %s", p)
			}
			visiting[p] = true
			for _, line := range strings.Split(string(b), "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				if err := check(line, depth+1); err != nil {
					return fmt.Errorf("acceptance script %s: %w", p, err)
				}
			}
			delete(visiting, p)
			frozen[p] = true
			return nil
		}
		return errors.New("unsupported acceptance command; use bounded grep/test/sh or a trusted /opt/foundry/ verifier")
	}
	for _, command := range commands {
		if err := check(command, 0); err != nil {
			return nil, err
		}
	}
	inputs := make([]string, 0, len(frozen))
	for p := range frozen {
		inputs = append(inputs, p)
	}
	sort.Strings(inputs)
	return inputs, nil
}

func imageVerifier(p string) bool {
	return strings.HasPrefix(p, "/opt/foundry/") && path.Clean(p) == p && p != "/opt/foundry/" && safePath(strings.TrimPrefix(p, "/")) == nil
}

func acceptanceRepoPath(p string) (string, error) {
	p = strings.TrimPrefix(p, "/workspace/")
	p = strings.TrimPrefix(p, "./")
	if err := safePath(p); err != nil {
		return "", fmt.Errorf("unsupported acceptance path: %w", err)
	}
	if strings.HasPrefix(p, "-") {
		return "", errors.New("acceptance path cannot begin with an option")
	}
	return p, nil
}

// Only plain ASCII literal words and single-quoted literal words are accepted.
// No expansion, operators, escaping, environment assignments or concatenation.
func acceptanceWords(command string) ([]string, error) {
	var words []string
	for i := 0; i < len(command); {
		if command[i] == ' ' || command[i] == '\t' {
			i++
			continue
		}
		start := i
		if command[i] == '\'' {
			i++
			start = i
			for i < len(command) && command[i] != '\'' {
				if command[i] < 32 || command[i] > 126 {
					return nil, errors.New("non-literal token")
				}
				i++
			}
			if i == len(command) {
				return nil, errors.New("unclosed literal")
			}
			words = append(words, command[start:i])
			i++
			if i < len(command) && command[i] != ' ' && command[i] != '\t' {
				return nil, errors.New("concatenated token")
			}
			continue
		}
		for i < len(command) && command[i] != ' ' && command[i] != '\t' {
			c := command[i]
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_./:=,+@%-", rune(c))) {
				return nil, errors.New("shell syntax is unsupported")
			}
			i++
		}
		words = append(words, command[start:i])
	}
	return words, nil
}
