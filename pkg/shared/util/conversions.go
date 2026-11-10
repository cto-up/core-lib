package util

import (
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// NullableTypes is a type constraint that limits the allowed pointer types
type NullableTypes interface {
	~string | ~int | ~int32 | ~int64 | ~float32 | ~float64 | ~bool | time.Time
}

// ToNullable converts a pointer of a basic type to its corresponding pgtype nullable type
func ToNullable[T NullableTypes](value *T) any {
	if value == nil {
		return getNullType[T]()
	}
	return getPresentType(value)
}

// Individual typed functions for when you need specific return types
func ToNullableUUID(value *uuid.UUID) pgtype.UUID {
	if value == nil {
		return pgtype.UUID{Valid: false}
	}
	uuid := pgtype.UUID{
		Bytes: *value,
		Valid: true,
	}
	return uuid
}

func ToNullableText(value *string) pgtype.Text {
	if value == nil {
		return pgtype.Text{Valid: false}
	}
	return pgtype.Text{String: *value, Valid: true}
}

func ToNullableBool(value *bool) pgtype.Bool {
	if value == nil {
		return pgtype.Bool{Valid: false}
	}
	return pgtype.Bool{Bool: *value, Valid: true}
}

func ToNullableTimestamptz(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{Valid: false}
	}
	return pgtype.Timestamptz{Time: *value, Valid: true}
}

// Helper functions for the generic version
func getNullType[T NullableTypes]() any {
	var zero T
	switch any(zero).(type) {
	case string:
		return pgtype.Text{Valid: false}
	case int, int32:
		return pgtype.Int4{Valid: false}
	case int64:
		return pgtype.Int8{Valid: false}
	case float32:
		return pgtype.Float4{Valid: false}
	case float64:
		return pgtype.Float8{Valid: false}
	case bool:
		return pgtype.Bool{Valid: false}
	case time.Time:
		return pgtype.Timestamp{Valid: false}
	default:
		panic("unsupported type")
	}
}

func getPresentType[T NullableTypes](value *T) any {
	switch v := any(*value).(type) {
	case string:
		return pgtype.Text{String: v, Valid: true}
	case int:
		return pgtype.Int4{Int32: int32(v), Valid: true}
	case int32:
		return pgtype.Int4{Int32: v, Valid: true}
	case int64:
		return pgtype.Int8{Int64: v, Valid: true}
	case float32:
		return pgtype.Float4{Float32: v, Valid: true}
	case float64:
		return pgtype.Float8{Float64: v, Valid: true}
	case bool:
		return pgtype.Bool{Bool: v, Valid: true}
	case time.Time:
		return pgtype.Timestamp{Time: v, Valid: true}
	default:
		panic("unsupported type")
	}
}

// PgNullableTypes is a type constraint that limits the allowed pgtype inputs
type PgNullableTypes interface {
	pgtype.Text | pgtype.Int4 | pgtype.Int8 | pgtype.Float4 | pgtype.Float8 |
		pgtype.Bool | pgtype.Timestamp | pgtype.Date
}

// FromNullable converts pgtype nullable types to pointers of Go primitive types.
// A T the value cannot be read as yields nil, like a NULL does.
func FromNullable[T any, P PgNullableTypes](value P) *T {
	switch v := any(value).(type) {
	case pgtype.UUID:
		if v.Valid {
			uuid, _ := uuid.FromBytes(v.Bytes[:])
			return pointerAs[T](&uuid)
		}
	case pgtype.Text:
		if v.Valid {
			return pointerAs[T](&v.String)
		}
	case pgtype.Int4:
		if v.Valid {
			switch any(*new(T)).(type) {
			case int:
				i := int(v.Int32)
				return pointerAs[T](&i)
			case int32:
				return pointerAs[T](&v.Int32)
			}
		}
	case pgtype.Int8:
		if v.Valid {
			i := int64(v.Int64)
			return pointerAs[T](&i)
		}
	case pgtype.Float4:
		if v.Valid {
			switch any(*new(T)).(type) {
			case float32:
				return pointerAs[T](&v.Float32)
			case float64:
				f := float64(v.Float32)
				return pointerAs[T](&f)
			}
		}
	case pgtype.Float8:
		if v.Valid {
			f := float64(v.Float64)
			return pointerAs[T](&f)
		}
	case pgtype.Bool:
		if v.Valid {
			return pointerAs[T](&v.Bool)
		}
	case pgtype.Timestamp:
		if v.Valid {
			return pointerAs[T](&v.Time)
		}
	case pgtype.Date:
		if v.Valid {
			return pointerAs[T](&v.Time)
		}
	}
	return nil
}

// pointerAs returns p as a *T, or nil when it points at another type.
func pointerAs[T any](p any) *T {
	t, _ := p.(*T)
	return t
}

// Type-specific versions for better type safety

func FromNullableText(value pgtype.Text) *string {
	if value.Valid {
		return &value.String
	}
	return nil
}

func FromNullableTimestamp(value pgtype.Timestamp) *time.Time {
	if value.Valid {
		return &value.Time
	}
	return nil
}
func FromNullableTimestamptz(value pgtype.Timestamptz) *time.Time {
	if value.Valid {
		return &value.Time
	}
	return nil
}
