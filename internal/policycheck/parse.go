// Package policycheck compares statically authored ipsets with subnet reservations.
// It performs no I/O and never consults live tailnet state.
package policycheck

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/tailscale/hujson"
)

type definitions struct {
	sets  map[string][]string
	hosts map[string]string
	order []string
}

func parse(data []byte) (definitions, error) {
	if !utf8.Valid(data) {
		return definitions{}, errors.New("policy must contain valid UTF-8")
	}
	value, err := hujson.Parse(data)
	if err != nil {
		// Parser errors may quote raw policy text, including identities.
		return definitions{}, errors.New("policy is not valid HuJSON")
	}
	root, ok := value.Value.(*hujson.Object)
	if !ok {
		return definitions{}, errors.New("policy must be an object")
	}
	result := definitions{sets: make(map[string][]string), hosts: make(map[string]string)}
	seen := make(map[string]bool)
	for _, member := range root.Members {
		// Successful HuJSON parsing guarantees string-literal object keys.
		literal, _ := member.Name.Value.(hujson.Literal)
		key := strings.ToLower(literal.String())
		if key != "ipsets" && key != "hosts" {
			continue
		}
		if seen[key] {
			return definitions{}, fmt.Errorf("policy has duplicate or ambiguous %s objects", key)
		}
		seen[key] = true
		if err = result.readObject(key, member.Value); err != nil {
			return definitions{}, err
		}
	}
	return result, nil
}

func (d *definitions) readObject(key string, value hujson.Value) error {
	object, ok := value.Value.(*hujson.Object)
	if !ok {
		return fmt.Errorf("policy %s must be an object", key)
	}
	seen := make(map[string]bool)
	for index, member := range object.Members {
		// Successful HuJSON parsing guarantees string-literal object keys.
		nameLiteral, _ := member.Name.Value.(hujson.Literal)
		name := nameLiteral.String()
		if seen[name] {
			return fmt.Errorf("policy %s definition %d duplicates an earlier name", key, index+1)
		}
		seen[name] = true
		if key == "hosts" {
			literal, valid := member.Value.Value.(hujson.Literal)
			if !valid || literal.Kind() != '"' {
				return fmt.Errorf("policy hosts definition %d must be a string", index+1)
			}
			d.hosts[name] = literal.String()
			continue
		}
		if !strings.HasPrefix(name, "ipset:") || strings.TrimSpace(strings.TrimPrefix(name, "ipset:")) == "" ||
			strings.TrimSpace(name) != name {
			return fmt.Errorf("policy ipsets definition %d needs an ipset: name", index+1)
		}
		members, err := readMembers(member.Value)
		if err != nil {
			return fmt.Errorf("policy ipsets definition %d: %w", index+1, err)
		}
		d.sets[name] = members
		d.order = append(d.order, name)
	}
	return nil
}

func readMembers(value hujson.Value) ([]string, error) {
	array, valid := value.Value.(*hujson.Array)
	if !valid {
		return nil, errors.New("members must be an array")
	}
	members := make([]string, len(array.Elements))
	for index, element := range array.Elements {
		literal, isString := element.Value.(hujson.Literal)
		if !isString || literal.Kind() != '"' {
			return nil, fmt.Errorf("member %d must be a string", index+1)
		}
		members[index] = literal.String()
	}
	return members, nil
}
