package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	maxGroupDepth = 40
	maxGroupItems = maxConfigEntries
)

type GroupItem struct {
	EntryName *string
	Group     *Group
}

type Group struct {
	Name  string
	Items []GroupItem
}

func (item *GroupItem) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		return errors.New("must be a configs name or group object")
	}
	if data[0] != '{' {
		var name string
		if err := json.Unmarshal(data, &name); err != nil {
			return errors.New("must be a configs name or group object")
		}
		item.EntryName = &name
		item.Group = nil
		return nil
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return errors.New("must be a configs name or group object")
	}
	rawName, ok := findJSONField(object, "name")
	if !ok {
		return errors.New("group object is missing required field name")
	}
	if bytes.Equal(bytes.TrimSpace(rawName), []byte("null")) {
		return errors.New("group name must be a string")
	}
	var name string
	if err := json.Unmarshal(rawName, &name); err != nil {
		return errors.New("group name must be a string")
	}

	var items []GroupItem
	if rawItems, ok := findJSONField(object, "groups"); ok {
		if bytes.Equal(bytes.TrimSpace(rawItems), []byte("null")) {
			return errors.New("group groups must be an array")
		}
		if err := json.Unmarshal(rawItems, &items); err != nil {
			return fmt.Errorf("group groups must be an array: %w", err)
		}
	}
	item.EntryName = nil
	item.Group = &Group{Name: name, Items: items}
	return nil
}

func validateGroups(items []GroupItem, names map[string]int) error {
	count := 0
	return validateGroupItems(items, names, "groups", 0, &count)
}

func validateGroupItems(items []GroupItem, names map[string]int, path string, parentDepth int, count *int) error {
	for i, item := range items {
		(*count)++
		if *count > maxGroupItems {
			return fmt.Errorf("groups contains more than %d menu items", maxGroupItems)
		}
		itemPath := fmt.Sprintf("%s[%d]", path, i)
		switch {
		case item.EntryName != nil:
			if _, exists := names[*item.EntryName]; !exists {
				return fmt.Errorf("%s must reference an existing configs name: %q", itemPath, *item.EntryName)
			}
		case item.Group != nil:
			depth := parentDepth + 1
			if depth > maxGroupDepth {
				return fmt.Errorf("%s exceeds the maximum group depth of %d", itemPath, maxGroupDepth)
			}
			if err := validateGroupItems(item.Group.Items, names, itemPath+".groups", depth, count); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%s must be a configs name or group object", itemPath)
		}
	}
	return nil
}

func validateGroupsJSON(raw json.RawMessage) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	count := 0
	if err := validateGroupArrayJSON(decoder, "groups", 0, &count); err != nil {
		return err
	}
	if token, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err != nil {
			return fmt.Errorf("groups: %w", err)
		}
		return fmt.Errorf("groups contains unexpected trailing token %v", token)
	}
	return nil
}

func validateGroupArrayJSON(decoder *json.Decoder, path string, parentDepth int, count *int) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("%s must be an array", path)
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '[' {
		return fmt.Errorf("%s must be an array", path)
	}
	for index := 0; decoder.More(); index++ {
		(*count)++
		if *count > maxGroupItems {
			return fmt.Errorf("groups contains more than %d menu items", maxGroupItems)
		}
		itemPath := fmt.Sprintf("%s[%d]", path, index)
		itemToken, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("%s: %w", itemPath, err)
		}
		switch value := itemToken.(type) {
		case string:
		case json.Delim:
			if value != '{' {
				return fmt.Errorf("%s must be a configs name or group object", itemPath)
			}
			if err := validateGroupObjectJSON(decoder, itemPath, parentDepth+1, count); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%s must be a configs name or group object", itemPath)
		}
	}
	if _, err := decoder.Token(); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func validateGroupObjectJSON(decoder *json.Decoder, path string, depth int, count *int) error {
	if depth > maxGroupDepth {
		return fmt.Errorf("%s exceeds the maximum group depth of %d", path, maxGroupDepth)
	}
	seenName := false
	seenGroups := false
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		key, ok := token.(string)
		if !ok {
			return fmt.Errorf("%s contains a non-string field name", path)
		}
		switch {
		case strings.EqualFold(key, "name"):
			if seenName {
				return fmt.Errorf("%s contains duplicate name fields", path)
			}
			seenName = true
			value, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("%s.name must be a string", path)
			}
			if _, ok := value.(string); !ok {
				return fmt.Errorf("%s.name must be a string", path)
			}
		case strings.EqualFold(key, "groups"):
			if seenGroups {
				return fmt.Errorf("%s contains duplicate groups fields", path)
			}
			seenGroups = true
			if err := validateGroupArrayJSON(decoder, path+".groups", depth, count); err != nil {
				return err
			}
		default:
			var ignored any
			if err := decoder.Decode(&ignored); err != nil {
				return fmt.Errorf("%s.%s: %w", path, key, err)
			}
		}
	}
	if _, err := decoder.Token(); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if !seenName {
		return fmt.Errorf("%s is missing required field name", path)
	}
	return nil
}

func findUniqueJSONField(object map[string]json.RawMessage, name, path string) (json.RawMessage, bool, error) {
	var found json.RawMessage
	foundKey := ""
	for key, raw := range object {
		if !strings.EqualFold(key, name) {
			continue
		}
		if foundKey != "" {
			return nil, false, fmt.Errorf("%s contains duplicate fields %s and %s", path, foundKey, key)
		}
		found = raw
		foundKey = key
	}
	return found, foundKey != "", nil
}
