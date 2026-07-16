package jsonstrict

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

func ValidateNoDuplicateKeys(data []byte) error {
	return validateValue(json.NewDecoder(bytes.NewReader(data)), "$")
}

func validateValue(decoder *json.Decoder, path string) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}

	switch delim {
	case '{':
		var keys []string
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("object key at %s is not a string", path)
			}
			for _, seen := range keys {
				if strings.EqualFold(seen, key) {
					return fmt.Errorf("duplicate key %q at %s", key, path)
				}
			}
			keys = append(keys, key)
			if err := validateValue(decoder, path+"."+key); err != nil {
				return err
			}
		}
	case '[':
		for index := 0; decoder.More(); index++ {
			if err := validateValue(decoder, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q at %s", delim, path)
	}

	_, err = decoder.Token()
	return err
}
