package util

import (
	"encoding/json"
)

// FromJSONB converts a JSON byte array to the specified type T
func FromJSONB[T any](data []byte) T {
	var result T
	if data == nil {
		return result
	}

	err := json.Unmarshal(data, &result)
	if err != nil {
		return result
	}

	return result
}
