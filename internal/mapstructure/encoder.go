// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT

package mapstructure

import (
	"encoding"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/mitchellh/mapstructure"
)

const (
	tagNameMapStructure = "mapstructure"
	optionSeparator     = ","
	optionOmitEmpty     = "omitempty"
	optionSquash        = "squash"
	optionRemain        = "remain"
	optionSkip          = "-"
)

var (
	errNonStringEncodedKey = errors.New("non string-encoded key")
	errUnsupportedKind     = errors.New("unsupported kind")
)

// tagInfo stores the mapstructure tag details.
type tagInfo struct {
	name      string
	omitEmpty bool
	squash    bool
}

// An Encoder takes structured data and converts it into an
// interface following the mapstructure tags.
type Encoder struct {
	config *EncoderConfig
}

// EncoderConfig is the configuration used to create a new encoder.
type EncoderConfig struct {
	// EncodeHook, if set, is a way to provide custom encoding. It
	// will be called before structs and primitive types.
	EncodeHook mapstructure.DecodeHookFunc
	// NilEmptyMap, if set, is a way to nil out empty maps.
	NilEmptyMap bool
	// OmitNilFields, if set, is a way to omit all nil struct fields even if omitempty isn't present.
	OmitNilFields bool
}

// New returns a new encoder for the configuration.
func New(cfg *EncoderConfig) *Encoder {
	return &Encoder{config: cfg}
}

// Encode takes the input and uses reflection to encode it to
// an interface based on the mapstructure spec.
func (e *Encoder) Encode(input any) (any, error) {
	return e.encode(reflect.ValueOf(input))
}

// encode processes the value based on the reflect.Kind.
func (e *Encoder) encode(value reflect.Value) (any, error) {
	if value.IsValid() {
		switch value.Kind() {
		case reflect.Interface, reflect.Pointer:
			return e.encode(value.Elem())
		case reflect.Map:
			return e.encodeMap(value)
		case reflect.Slice, reflect.Array:
			return e.encodeSlice(value)
		case reflect.Struct:
			return e.encodeStruct(value)
		default:
			return e.encodeHook(value)
		}
	}
	return nil, nil
}

// encodeHook calls the EncodeHook in the EncoderConfig with the value passed in.
// This is called before processing structs and for primitive data types.
func (e *Encoder) encodeHook(value reflect.Value) (any, error) {
	if e.config != nil && e.config.EncodeHook != nil {
		out, err := mapstructure.DecodeHookExec(e.config.EncodeHook, value, value)
		if err != nil {
			return nil, fmt.Errorf("error running encode hook: %w", err)
		}
		return out, nil
	}
	return value.Interface(), nil
}

// encodeStruct encodes the struct by iterating over the fields, getting the
// mapstructure tagInfo for each exported field, and encoding the value.
func (e *Encoder) encodeStruct(value reflect.Value) (any, error) {
	if value.Kind() != reflect.Struct {
		return nil, &reflect.ValueError{
			Method: "encodeStruct",
			Kind:   value.Kind(),
		}
	}
	out, err := e.encodeHook(value)
	if err != nil {
		return nil, err
	}
	value = reflect.ValueOf(out)
	// if the output of encodeHook is no longer a struct,
	// call encode against it.
	if value.Kind() != reflect.Struct {
		return e.encode(value)
	}
	result := make(map[string]any)
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		if field.CanInterface() {
			info := getTagInfo(value.Type().Field(i))
			if (info.omitEmpty && field.IsZero()) || info.name == optionSkip {
				continue
			}
			encoded, err := e.encode(field)
			if err != nil {
				if errors.Is(err, errUnsupportedKind) {
					continue
				}
				return nil, fmt.Errorf("error encoding field %q: %w", info.name, err)
			}
			if e.config.OmitNilFields && encoded == nil {
				continue
			}
			if info.squash {
				if m, ok := encoded.(map[string]any); ok {
					for k, v := range m {
						result[k] = v
					}
				}
			} else {
				result[info.name] = encoded
			}
		}
	}
	return result, nil
}

// encodeSlice iterates over the slice and encodes each of the elements.
func (e *Encoder) encodeSlice(value reflect.Value) (any, error) {
	if value.Kind() != reflect.Slice && value.Kind() != reflect.Array {
		return nil, &reflect.ValueError{
			Method: "encodeSlice",
			Kind:   value.Kind(),
		}
	}
	result := make([]any, value.Len())
	for i := 0; i < value.Len(); i++ {
		var err error
		if result[i], err = e.encode(value.Index(i)); err != nil {
			return nil, fmt.Errorf("error encoding element in slice at index %d: %w", i, err)
		}
	}
	return result, nil
}

// encodeMap encodes a map by encoding the key and value. Returns errNonStringEncodedKey
// if the key is not encoded into a string.
func (e *Encoder) encodeMap(value reflect.Value) (any, error) {
	if value.Kind() != reflect.Map {
		return nil, &reflect.ValueError{
			Method: "encodeMap",
			Kind:   value.Kind(),
		}
	}
	result := make(map[string]any)
	iterator := value.MapRange()
	for iterator.Next() {
		encoded, err := e.encode(iterator.Key())
		if err != nil {
			return nil, fmt.Errorf("error encoding key: %w", err)
		}
		key, ok := encoded.(string)
		if !ok {
			return nil, fmt.Errorf("%w key %q, kind: %v", errNonStringEncodedKey, iterator.Key().Interface(), iterator.Key().Kind())
		}
		if _, ok = result[key]; ok {
			return nil, fmt.Errorf("duplicate key %q while encoding", key)
		}
		if result[key], err = e.encode(iterator.Value()); err != nil {
			return nil, fmt.Errorf("error encoding map value for key %q: %w", key, err)
		}
	}
	if e.config.NilEmptyMap && len(result) == 0 {
		return nil, nil
	}
	return result, nil
}

// getTagInfo looks up the mapstructure tag and uses that if available.
// Uses the lowercase field if not found. Checks for omitempty and squash.
func getTagInfo(field reflect.StructField) *tagInfo {
	info := tagInfo{}
	if tag, ok := field.Tag.Lookup(tagNameMapStructure); ok {
		options := strings.Split(tag, optionSeparator)
		info.name = options[0]
		if len(options) > 1 {
			for _, option := range options[1:] {
				switch option {
				case optionOmitEmpty:
					info.omitEmpty = true
				case optionSquash, optionRemain:
					info.squash = true
				}
			}
		}
	} else {
		info.name = strings.ToLower(field.Name)
	}
	return &info
}

// TextMarshalerHookFunc returns a DecodeHookFuncValue that checks
// for the encoding.TextMarshaler interface and calls the MarshalText
// function if found.
func TextMarshalerHookFunc() mapstructure.DecodeHookFuncValue {
	return func(from reflect.Value, _ reflect.Value) (any, error) {
		if !from.IsValid() {
			return nil, nil
		}
		marshaler, ok := from.Interface().(encoding.TextMarshaler)
		if !ok {
			return from.Interface(), nil
		}
		out, err := marshaler.MarshalText()
		if err != nil {
			return nil, err
		}
		return string(out), nil
	}
}

// NilHookFunc returns a DecodeHookFuncValue that checks if the value matches the type and nils it out. Allows specific
// types to be omitted.
func NilHookFunc[T any]() mapstructure.DecodeHookFuncValue {
	return nilHookFunc[T](false)
}

// NilZeroValueHookFunc returns a DecodeHookFuncValue that only nils the field if it's a zero value.
func NilZeroValueHookFunc[T any]() mapstructure.DecodeHookFuncValue {
	return nilHookFunc[T](true)
}

func nilHookFunc[T any](onlyIfZero bool) mapstructure.DecodeHookFuncValue {
	return func(from reflect.Value, _ reflect.Value) (any, error) {
		if !from.IsValid() {
			return nil, nil
		}
		_, ok := from.Interface().(T)
		if ok && (!onlyIfZero || from.IsZero()) {
			return nil, nil
		}
		return from.Interface(), nil
	}
}

// UnsupportedKindHookFunc returns a DecodeHookFuncValue that checks that the kind isn't one unsupported by the YAML
// encoder.
func UnsupportedKindHookFunc() mapstructure.DecodeHookFuncValue {
	return func(from reflect.Value, _ reflect.Value) (any, error) {
		if !from.IsValid() {
			return nil, nil
		}
		switch from.Kind() {
		case reflect.Chan, reflect.Func:
			return nil, fmt.Errorf("%w: %s", errUnsupportedKind, from.Kind())
		default:
			return from.Interface(), nil
		}
	}
}
