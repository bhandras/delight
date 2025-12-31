package actor

import "reflect"

// isNilInterface returns true if the provided interface value is nil, including
// the common case where the interface is non-nil but holds a nil concrete
// pointer.
func isNilInterface(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Slice, reflect.Map, reflect.Func, reflect.Chan:
		return rv.IsNil()
	default:
		return false
	}
}

