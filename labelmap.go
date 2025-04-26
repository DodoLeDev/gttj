package main

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// LabelMap represents the structure of the label-map.yaml file
type LabelMap struct {
	Mappings []LabelMapping `yaml:"mappings"`
	debug    bool
}

// LabelMapping represents a single locale mapping in the label-map.yaml file
type LabelMapping struct {
	Locale     string            `yaml:"locale"`
	Roles      map[string]string `yaml:"roles"`
	Keywords   map[string]string `yaml:"keywords"`
	Categories map[string]string `yaml:"categories"`
	Ignore     []string          `yaml:"ignore"`
}

// ReadLabelMap reads and parses the label-map.yaml file
func ReadLabelMap(filePath string, debug bool) (*LabelMap, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read label map file: %w", err)
	}

	var labelMap LabelMap
	if err := yaml.Unmarshal(data, &labelMap); err != nil {
		return nil, fmt.Errorf("failed to parse label map file: %w", err)
	}

	labelMap.debug = debug
	return &labelMap, nil
}

// GetMappingByLocale returns the mapping for a specific locale
func (lm *LabelMap) GetMappingByLocale(locale string) *LabelMapping {
	for _, mapping := range lm.Mappings {
		if mapping.Locale == locale {
			return &mapping
		}
	}
	return nil
}

// debugLog logs a message if debug mode is enabled
func (lm *LabelMap) debugLog(format string, args ...interface{}) {
	if lm.debug {
		fmt.Printf("[DEBUG] "+format+"\n", args...)
	}
}

// MapLabel maps a Gmail label to its JMAP equivalent based on the locale
// Returns the mapped label, whether it was mapped, and whether it's a role mapping
func (lm *LabelMap) MapLabel(locale, label string) (string, bool, bool) {
	mapping := lm.GetMappingByLocale(locale)
	if mapping == nil {
		return "", false, false
	}

	// Check if the label is in the ignore list
	for _, ignored := range mapping.Ignore {
		if ignored == label {
			lm.debugLog("Ignoring label: %s", label)
			return "", false, false
		}
	}

	// Check roles
	if mapped, ok := mapping.Roles[label]; ok {
		lm.debugLog("Mapped role label: %s -> %s", label, mapped)
		return mapped, true, true
	}

	// Check keywords
	if mapped, ok := mapping.Keywords[label]; ok {
		lm.debugLog("Mapped keyword label: %s -> %s", label, mapped)
		return mapped, true, false
	}

	// Check categories
	if mapped, ok := mapping.Categories[label]; ok {
		lm.debugLog("Mapped category label: %s -> %s", label, mapped)
		return mapped, true, false
	}

	lm.debugLog("No mapping found for label: %s", label)
	return "", false, false
}
