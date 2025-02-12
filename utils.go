package distcluststore

func Zeroed[T any](v *T) T {
	if v == nil {
		return *new(T)
	}
	return *v
}

func Contains[T comparable, U any](m map[T]U, key T) bool {
	_, ok := m[key]
	return ok
}
