package types

import "context"

type DynamicResolver interface {
	Resolve(ctx context.Context, fieldDefinition FieldDefinition, pathSegment PathSegment, args map[string]interface{}) (interface{}, error)
	HasScalarValue() bool
}
