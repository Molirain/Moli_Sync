package config

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"
)

var durationType = reflect.TypeOf(Duration(0))

// applyEnv walks the configuration struct and copies every MOLISYNC_*
// environment variable into its field. The environment is applied after the
// YAML file, so it always wins.
func applyEnv(root any) error {
	value := reflect.ValueOf(root)
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return fmt.Errorf("configuration must not be nil")
		}
		value = value.Elem()
	}
	return applyEnvValue(value, "")
}

func applyEnvValue(value reflect.Value, prefix string) error {
	if value.Kind() != reflect.Struct {
		return fmt.Errorf("configuration must be a struct")
	}
	valueType := value.Type()
	for i := 0; i < valueType.NumField(); i++ {
		field := valueType.Field(i)
		fieldValue := value.Field(i)

		if field.Type.Kind() == reflect.Struct && field.Type != durationType {
			nextPrefix := prefix + strings.ToUpper(field.Name) + "_"
			if err := applyEnvValue(fieldValue, nextPrefix); err != nil {
				return err
			}
			continue
		}

		name := field.Tag.Get("env")
		if name == "" {
			continue
		}
		raw, ok := os.LookupEnv(name)
		if !ok {
			continue
		}
		if err := setFromString(fieldValue, raw); err != nil {
			return fmt.Errorf("environment variable %s: %w", name, err)
		}
	}
	return nil
}

func setFromString(value reflect.Value, raw string) error {
	if value.Type() == durationType {
		parsed, err := time.ParseDuration(strings.TrimSpace(raw))
		if err != nil {
			return fmt.Errorf("invalid duration %q", raw)
		}
		value.SetInt(int64(parsed))
		return nil
	}

	switch value.Kind() {
	case reflect.String:
		value.SetString(raw)
	case reflect.Bool:
		parsed, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return fmt.Errorf("invalid boolean %q", raw)
		}
		value.SetBool(parsed)
	case reflect.Int, reflect.Int64:
		parsed, err := strconv.ParseInt(strings.TrimSpace(raw), 10, value.Type().Bits())
		if err != nil {
			return fmt.Errorf("invalid integer %q", raw)
		}
		value.SetInt(parsed)
	case reflect.Slice:
		if value.Type().Elem().Kind() != reflect.String {
			return fmt.Errorf("unsupported slice type %s", value.Type())
		}
		items := strings.Split(raw, ",")
		result := reflect.MakeSlice(value.Type(), 0, len(items))
		for _, item := range items {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			result = reflect.Append(result, reflect.ValueOf(item))
		}
		value.Set(result)
	default:
		return fmt.Errorf("unsupported configuration type %s", value.Type())
	}
	return nil
}
