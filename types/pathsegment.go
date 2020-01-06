package types

import "reflect"

type PathSegment struct {
	Parent   *PathSegment
	Value    interface{}
	Resolver reflect.Value
}

func (p *PathSegment) ToSlice() []interface{} {
	if p == nil {
		return nil
	}
	return append(p.Parent.ToSlice(), p.Value)
}
