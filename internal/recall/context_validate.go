package recall

import (
	"fmt"
	"unicode/utf8"
)

func validateContextDocument(role string, document Document) error {
	if document.Path == "" {
		return fmt.Errorf("recall: context %s document has empty path", role)
	}
	for _, field := range []struct {
		label string
		value string
	}{
		{label: "path", value: document.Path},
		{label: "title", value: document.Title},
		{label: "body", value: document.Body},
	} {
		if err := validateContextString(role+" "+field.label, field.value); err != nil {
			return err
		}
	}
	for index, metadata := range document.Metadata {
		if err := validateContextString(fmt.Sprintf("%s metadata %d property", role, index), metadata.Property); err != nil {
			return err
		}
		if err := validateContextString(fmt.Sprintf("%s metadata %d value", role, index), metadata.Value); err != nil {
			return err
		}
	}
	return nil
}

func validateContextString(label, value string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("recall: context %s is not valid UTF-8", label)
	}
	return nil
}
