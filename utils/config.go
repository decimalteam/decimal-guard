package utils

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

func init() {
	godotenv.Load()
}

// TODO: Read from some remote config storage instead of environment variables.

// LoadConfig reflects and scans all fields from specified `config` object and reads
// config parameters for all fields which specify `env` tag. If a field also contains
// "default" tag then it's value will be used as default if reading config parameter is failed.
func LoadConfig(config interface{}) (err error) {
	if config == nil {
		err = fmt.Errorf("unable to load config when nil specified")
		return
	}
	t := reflect.TypeOf(config).Elem()
	v := reflect.ValueOf(config).Elem()
	// Reflect type of specified `config` object and check it's kind
	if t.Kind() != reflect.Struct {
		err = fmt.Errorf("invalid config type (`%s` instead of `%s`)", t.Kind().String(), reflect.Struct)
		return
	}
	// Iterate over all fields and try to read them as config parameters
	for i, ct, cv := 0, t.NumField(), v.NumField(); i < ct && i < cv; i++ {
		ft, fv := t.Field(i), v.Field(i)
		// Check `env` tag
		var env string
		if v, ok := ft.Tag.Lookup("env"); ok && len(v) > 0 {
			env = v
		} else {
			// Tag `env` is not specified for the config struct field so just skip it
			continue
		}
		// Ensure that config struct field is valid and can be set
		if !fv.IsValid() || !fv.CanSet() {
			// TODO: Log better
			err = fmt.Errorf("invalid config type")
			return
		}
		switch fv.Kind() {
		case reflect.String:
			var value string
			value, err = ReadConfigString(env)
			if err == nil {
				fv.SetString(value)
			}
		case reflect.Bool:
			var value bool
			value, err = ReadConfigBool(env)
			if err == nil {
				fv.SetBool(value)
			}
		case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Int:
			var value int64
			value, err = ReadConfigInt64(env)
			if err == nil {
				fv.SetInt(value)
			}
		case reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uint:
			var value uint64
			value, err = ReadConfigUint64(env)
			if err == nil {
				fv.SetUint(value)
			}
		case reflect.Float32, reflect.Float64:
			var value float64
			value, err = ReadConfigFloat64(env)
			if err == nil {
				fv.SetFloat(value)
			}
		default:
			// TODO: Log better
			err = fmt.Errorf("invalid config type")
		}
		if err != nil {
			defvalue := ft.Tag.Get("default")
			mandatory := strings.Contains(ft.Tag.Get("mandatory"), "true")
			if len(defvalue) > 0 {
				switch fv.Kind() {
				case reflect.String:
					fv.SetString(defvalue)
				case reflect.Bool:
					fv.SetBool(strings.Contains(defvalue, "true"))
				case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Int:
					v, err := strconv.ParseInt(defvalue, 10, 64)
					if err != nil {
						err = fmt.Errorf("unable to parse config param %s with value \"%s\" to int64", env, defvalue)
						// TODO: Warn at least
						continue
					}
					fv.SetInt(v)
				case reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uint:
					v, err := strconv.ParseUint(defvalue, 10, 64)
					if err != nil {
						err = fmt.Errorf("unable to parse config param %s with value \"%s\" to uint64", env, defvalue)
						// TODO: Warn at least
						continue
					}
					fv.SetUint(v)
				case reflect.Float32, reflect.Float64:
					v, err := strconv.ParseFloat(defvalue, 64)
					if err != nil {
						err = fmt.Errorf("unable to parse config param %s with value \"%s\" to float64", env, defvalue)
						// TODO: Warn at least
						continue
					}
					fv.SetFloat(v)
				default:
					// TODO: Log better
					err = fmt.Errorf("invalid config type")
				}
			} else if mandatory {
				// Mandatory configuration is missed
				return
			}
			// Ignore error since the config param has default value or is not mandatory
			err = nil
		}
		// TODO: Log carefully
		// fmt.Println(ft.Tag.Get("env"), ft.Tag.Get("default"))
	}
	return nil
}

// ReadConfigString reads config parameter as string from a config storage.
func ReadConfigString(key string) (string, error) {
	s, ok := os.LookupEnv(key)
	if !ok {
		err := fmt.Errorf("config param %s is not found", key)
		return "", err
	}
	// if len(s) == 0 {
	// 	err := fmt.Errorf("config param %s is empty", key)
	// 	return "", err
	// }
	return s, nil
}

// ReadConfigBool reads config parameter as bool from a config storage.
func ReadConfigBool(key string) (bool, error) {
	v, err := ReadConfigString(key)
	if err != nil {
		return false, err
	}
	return strings.Contains(v, "true"), nil
}

// ReadConfigByte reads config parameter as byte from a config storage.
func ReadConfigByte(key string) (byte, error) {
	return ReadConfigUint8(key)
}

// ReadConfigInt8 reads config parameter as int8 from a config storage.
func ReadConfigInt8(key string) (int8, error) {
	s, err := ReadConfigString(key)
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseInt(s, 10, 8)
	if err != nil {
		err = fmt.Errorf("unable to parse config param %s with value \"%s\" to int8", key, s)
		return 0, err
	}
	return int8(v), nil
}

// ReadConfigUint8 reads config parameter as uint8 from a config storage.
func ReadConfigUint8(key string) (uint8, error) {
	s, err := ReadConfigString(key)
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseUint(s, 10, 8)
	if err != nil {
		err = fmt.Errorf("unable to parse config param %s with value \"%s\" to uint8", key, s)
		return 0, err
	}
	return uint8(v), nil
}

// ReadConfigInt16 reads config parameter as int16 from a config storage.
func ReadConfigInt16(key string) (int16, error) {
	s, err := ReadConfigString(key)
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseInt(s, 10, 16)
	if err != nil {
		err = fmt.Errorf("unable to parse config param %s with value \"%s\" to int16", key, s)
		return 0, err
	}
	return int16(v), nil
}

// ReadConfigUint16 reads config parameter as uint16 from a config storage.
func ReadConfigUint16(key string) (uint16, error) {
	s, err := ReadConfigString(key)
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseUint(s, 10, 16)
	if err != nil {
		err = fmt.Errorf("unable to parse config param %s with value \"%s\" to uint16", key, s)
		return 0, err
	}
	return uint16(v), nil
}

// ReadConfigInt32 reads config parameter as int32 from a config storage.
func ReadConfigInt32(key string) (int32, error) {
	s, err := ReadConfigString(key)
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		err = fmt.Errorf("unable to parse config param %s with value \"%s\" to int32", key, s)
		return 0, err
	}
	return int32(v), nil
}

// ReadConfigUint32 reads config parameter as uint32 from a config storage.
func ReadConfigUint32(key string) (uint32, error) {
	s, err := ReadConfigString(key)
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		err = fmt.Errorf("unable to parse config param %s with value \"%s\" to uint32", key, s)
		return 0, err
	}
	return uint32(v), nil
}

// ReadConfigInt64 reads config parameter as int64 from a config storage.
func ReadConfigInt64(key string) (int64, error) {
	s, err := ReadConfigString(key)
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		err = fmt.Errorf("unable to parse config param %s with value \"%s\" to int64", key, s)
		return 0, err
	}
	return int64(v), nil
}

// ReadConfigUint64 reads config parameter as uint64 from a config storage.
func ReadConfigUint64(key string) (uint64, error) {
	s, err := ReadConfigString(key)
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		err = fmt.Errorf("unable to parse config param %s with value \"%s\" to uint64", key, s)
		return 0, err
	}
	return uint64(v), nil
}

// ReadConfigInt reads config parameter as int from a config storage.
func ReadConfigInt(key string) (int, error) {
	s, err := ReadConfigString(key)
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		err = fmt.Errorf("unable to parse config param %s with value \"%s\" to int", key, s)
		return 0, err
	}
	return int(v), nil
}

// ReadConfigUint reads config parameter as uint from a config storage.
func ReadConfigUint(key string) (uint, error) {
	s, err := ReadConfigString(key)
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		err = fmt.Errorf("unable to parse config param %s with value \"%s\" to uint", key, s)
		return 0, err
	}
	return uint(v), nil
}

// ReadConfigFloat32 reads config parameter as float32 from a config storage.
func ReadConfigFloat32(key string) (float32, error) {
	s, err := ReadConfigString(key)
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseFloat(s, 32)
	if err != nil {
		err = fmt.Errorf("unable to parse config param %s with value \"%s\" to float32", key, s)
		return 0, err
	}
	return float32(v), nil
}

// ReadConfigFloat64 reads config parameter as float64 from a config storage.
func ReadConfigFloat64(key string) (float64, error) {
	s, err := ReadConfigString(key)
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		err = fmt.Errorf("unable to parse config param %s with value \"%s\" to float64", key, s)
		return 0, err
	}
	return float64(v), nil
}
