package v1alpha1

import (
	"encoding/json"
	"fmt"
)

// ArgValue is the value of a command line argument. It is either a single string or a list of strings, a list renders
// the flag once per entry in the given order, for flags that can be repeated.
type ArgValue []string

func (a *ArgValue) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*a = ArgValue{single}
		return nil
	}
	var list []string
	if err := json.Unmarshal(data, &list); err != nil {
		return fmt.Errorf("an argument must be a string or a list of strings: %w", err)
	}
	*a = list
	return nil
}

func (a ArgValue) MarshalJSON() ([]byte, error) {
	var value any = []string(a)
	if len(a) == 1 {
		value = a[0]
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal argument: %w", err)
	}
	return data, nil
}
