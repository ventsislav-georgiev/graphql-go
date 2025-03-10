package resolvable

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/graph-gophers/graphql-go/decode"
	"github.com/graph-gophers/graphql-go/internal/exec/packer"
	"github.com/graph-gophers/graphql-go/types"
)

type Schema struct {
	*Meta
	types.Schema
	Query        Resolvable
	Mutation     Resolvable
	Subscription Resolvable
	Resolver     reflect.Value
}

type Resolvable interface {
	isResolvable()
}

type Object struct {
	Name           string
	Fields         map[string]*Field
	TypeAssertions map[string]*TypeAssertion
}

type Field struct {
	types.FieldDefinition
	TypeName           string
	MethodIndex        int
	FieldIndex         []int
	HasContext         bool
	HasFieldDefinition bool
	HasPathSegment     bool
	HasSelectedFields  bool
	HasError           bool
	HasArgs            bool
	ArgsPacker         *packer.StructPacker
	ValueExec          Resolvable
	TraceLabel         string
	Resolver           *reflect.Value
}

func (f *Field) UseMethodResolver() bool {
	return len(f.FieldIndex) == 0
}

type TypeAssertion struct {
	MethodIndex int
	TypeExec    Resolvable
}

type List struct {
	Elem Resolvable
}

type Scalar struct{}

func (*Object) isResolvable() {}
func (*List) isResolvable()   {}
func (*Scalar) isResolvable() {}

func ApplyResolver(s *types.Schema, resolver interface{}) (*Schema, error) {
	if resolver == nil {
		return &Schema{Meta: newMeta(s), Schema: *s}, nil
	}

	b := newBuilder(s)

	var query, mutation, subscription Resolvable

	if t, ok := s.EntryPoints["query"]; ok {
		if err := b.assignExec(nil, &query, t, reflect.TypeOf(resolver)); err != nil {
			return nil, err
		}
	}

	if t, ok := s.EntryPoints["mutation"]; ok {
		if err := b.assignExec(nil, &mutation, t, reflect.TypeOf(resolver)); err != nil {
			return nil, err
		}
	}

	if t, ok := s.EntryPoints["subscription"]; ok {
		if err := b.assignExec(nil, &subscription, t, reflect.TypeOf(resolver)); err != nil {
			return nil, err
		}
	}

	if err := b.finish(); err != nil {
		return nil, err
	}

	return &Schema{
		Meta:         newMeta(s),
		Schema:       *s,
		Resolver:     reflect.ValueOf(resolver),
		Query:        query,
		Mutation:     mutation,
		Subscription: subscription,
	}, nil
}

type execBuilder struct {
	schema        *types.Schema
	resMap        map[typePair]*resMapEntry
	packerBuilder *packer.Builder
}

type typePair struct {
	graphQLType  types.Type
	resolverType reflect.Type
}

type resMapEntry struct {
	exec    Resolvable
	targets []*Resolvable
}

func newBuilder(s *types.Schema) *execBuilder {
	return &execBuilder{
		schema:        s,
		resMap:        make(map[typePair]*resMapEntry),
		packerBuilder: packer.NewBuilder(),
	}
}

func (b *execBuilder) finish() error {
	for _, entry := range b.resMap {
		for _, target := range entry.targets {
			*target = entry.exec
		}
	}

	return b.packerBuilder.Finish()
}

func (b *execBuilder) assignExec(parentField *Field, target *Resolvable, t types.Type, resolverType reflect.Type) error {
	k := typePair{t, resolverType}
	ref, ok := b.resMap[k]
	if !ok {
		ref = &resMapEntry{}
		b.resMap[k] = ref
		var err error
		ref.exec, err = b.makeExec(parentField, t, resolverType)
		if err != nil {
			return err
		}
	}
	ref.targets = append(ref.targets, target)
	return nil
}

func (b *execBuilder) makeExec(parentField *Field, t types.Type, resolverType reflect.Type) (Resolvable, error) {
	var nonNull bool
	t, nonNull = unwrapNonNull(t)

	switch t := t.(type) {
	case *types.ObjectTypeDefinition:
		return b.makeObjectExec(parentField, t.Name, t.Fields, nil, nonNull, resolverType)

	case *types.InterfaceTypeDefinition:
		return b.makeObjectExec(parentField, t.Name, t.Fields, t.PossibleTypes, nonNull, resolverType)

	case *types.Union:
		return b.makeObjectExec(parentField, t.Name, nil, t.UnionMemberTypes, nonNull, resolverType)
	}

	if !nonNull && resolverType.Kind() != reflect.Interface {
		if resolverType.Kind() != reflect.Ptr {
			return nil, fmt.Errorf("%s is not a pointer", resolverType)
		}
		resolverType = resolverType.Elem()
	}

	switch t := t.(type) {
	case *types.ScalarTypeDefinition:
		return makeScalarExec(t, resolverType)

	case *types.EnumTypeDefinition:
		return &Scalar{}, nil

	case *types.List:
		if resolverType.Kind() != reflect.Slice {
			return nil, fmt.Errorf("%s is not a slice", resolverType)
		}
		e := &List{}
		if err := b.assignExec(parentField, &e.Elem, t.OfType, resolverType.Elem()); err != nil {
			return nil, err
		}
		return e, nil

	default:
		panic("invalid type: " + t.String())
	}
}

func makeScalarExec(t *types.ScalarTypeDefinition, resolverType reflect.Type) (Resolvable, error) {
	implementsType := false
	switch r := reflect.New(resolverType).Interface().(type) {
	case *int32:
		implementsType = t.Name == "Int"
	case *float64:
		implementsType = t.Name == "Float"
	case *string:
		implementsType = t.Name == "String"
	case *bool:
		implementsType = t.Name == "Boolean"
	case decode.Unmarshaler:
		implementsType = r.ImplementsGraphQLType(t.Name)
	case interface{}:
		// Skip validation as it cannot be done from type inspection
		implementsType = true
	}

	if !implementsType {
		return nil, fmt.Errorf("can not use %s as %s", resolverType, t.Name)
	}
	return &Scalar{}, nil
}

var dynamicResolverType = reflect.TypeOf((*types.DynamicResolver)(nil)).Elem()

func (b *execBuilder) makeObjectExec(parentField *Field, typeName string, fields types.FieldsDefinition, possibleTypes []*types.ObjectTypeDefinition,
	nonNull bool, resolverType reflect.Type) (*Object, error) {
	if parentField != nil && fields != nil {
		for _, f := range fields {
			f.Parent = &parentField.FieldDefinition
		}
	}

	if !nonNull {
		if resolverType.Kind() != reflect.Ptr && resolverType.Kind() != reflect.Interface {
			return nil, fmt.Errorf("%s is not a pointer or interface", resolverType)
		}
	}

	Fields := make(map[string]*Field)
	rt := unwrapPtr(resolverType)
	fieldsCount := fieldCount(rt, map[string]int{})
	for _, f := range fields {
		var fieldIndex []int
		methodIndex := findMethod(resolverType, f.Name)
		if b.schema.UseFieldResolvers && methodIndex == -1 {
			if fieldsCount[strings.ToLower(stripUnderscore(f.Name))] > 1 {
				return nil, fmt.Errorf("%s does not resolve %q: ambiguous field %q", resolverType, typeName, f.Name)
			}
			fieldIndex = findField(rt, f.Name, []int{})
		}

		var fieldResolver *reflect.Value
		fieldResolverType := resolverType
		if methodIndex == -1 && len(fieldIndex) == 0 && b.schema.DefaultResolversProvider != nil {
			resolverInfo := b.schema.DefaultResolversProvider.GetResolver(*f)
			if resolverInfo != nil {
				providedResolver := reflect.ValueOf(resolverInfo.Resolver)
				methodIndex = findMethod(providedResolver.Type(), resolverInfo.MethodName)
				if methodIndex != -1 {
					fieldResolver = &providedResolver
					fieldResolverType = providedResolver.Type()
				}
			}
		}

		if methodIndex == -1 && len(fieldIndex) == 0 && b.schema.UseDynamicResolvers && fieldResolverType == dynamicResolverType {
			methodIndex = findMethod(fieldResolverType, "Resolve")
		}

		if methodIndex == -1 && len(fieldIndex) == 0 {
			hint := ""
			if findMethod(reflect.PtrTo(fieldResolverType), f.Name) != -1 {
				hint = " (hint: the method exists on the pointer type)"
			}
			return nil, fmt.Errorf("%s does not resolve %q: missing method for field %q%s", fieldResolverType, typeName, f.Name, hint)
		}

		var m reflect.Method
		var sf reflect.StructField
		if methodIndex != -1 {
			m = fieldResolverType.Method(methodIndex)
		} else {
			sf = rt.FieldByIndex(fieldIndex)
		}

		methodHasReceiver := fieldResolverType.Kind() != reflect.Interface
		fe, err := b.makeFieldExec(typeName, f, m, sf, methodIndex, fieldIndex, methodHasReceiver, fieldResolver, parentField)
		if err != nil {
			return nil, fmt.Errorf("%s\n\tused by (%s).%s", err, fieldResolverType, m.Name)
		}
		Fields[f.Name] = fe
	}

	// Check type assertions when
	//	1) using method resolvers
	//	2) Or resolver is not an interface type
	typeAssertions := make(map[string]*TypeAssertion)
	if !b.schema.UseFieldResolvers || resolverType.Kind() != reflect.Interface {
		for _, impl := range possibleTypes {
			methodIndex := findMethod(resolverType, "To"+impl.Name)
			if methodIndex == -1 {
				return nil, fmt.Errorf("%s does not resolve %q: missing method %q to convert to %q", resolverType, typeName, "To"+impl.Name, impl.Name)
			}
			if resolverType.Method(methodIndex).Type.NumOut() != 2 {
				return nil, fmt.Errorf("%s does not resolve %q: method %q should return a value and a bool indicating success", resolverType, typeName, "To"+impl.Name)
			}
			a := &TypeAssertion{
				MethodIndex: methodIndex,
			}
			methodValue := resolverType.Method(methodIndex)
			if err := b.assignExec(nil, &a.TypeExec, impl, methodValue.Type.Out(0)); err != nil {
				return nil, err
			}
			typeAssertions[impl.Name] = a
		}
	}

	return &Object{
		Name:           typeName,
		Fields:         Fields,
		TypeAssertions: typeAssertions,
	}, nil
}

var contextType = reflect.TypeOf((*context.Context)(nil)).Elem()
var fieldDefinitionType = reflect.TypeOf((*types.FieldDefinition)(nil)).Elem()
var pathSegmentType = reflect.TypeOf((*types.PathSegment)(nil)).Elem()
var selectedFieldsType = reflect.TypeOf((*types.SelectedFields)(nil)).Elem()
var errorType = reflect.TypeOf((*error)(nil)).Elem()

func (b *execBuilder) makeFieldExec(typeName string, f *types.FieldDefinition, m reflect.Method, sf reflect.StructField,
	methodIndex int, fieldIndex []int, methodHasReceiver bool, resolver *reflect.Value, parentField *Field) (*Field, error) {

	var argsPacker *packer.StructPacker
	var hasArgs bool
	var hasError bool
	var hasContext bool
	var hasFieldDefinition bool
	var hasPathSegment bool
	var hasSelectedFields bool

	// Validate resolver method only when there is one
	if methodIndex != -1 {
		in := make([]reflect.Type, m.Type.NumIn())
		for i := range in {
			in[i] = m.Type.In(i)
		}
		if methodHasReceiver {
			in = in[1:] // first parameter is receiver
		}

		hasContext = len(in) > 0 && in[0] == contextType
		if hasContext {
			in = in[1:]
		}

		hasFieldDefinition = len(in) > 0 && in[0] == fieldDefinitionType
		if hasFieldDefinition {
			in = in[1:]
		}

		hasPathSegment = len(in) > 0 && in[0] == pathSegmentType
		if hasPathSegment {
			in = in[1:]
		}

		hasSelectedFields = len(in) > 0 && in[0] == selectedFieldsType
		if hasSelectedFields {
			in = in[1:]
		}

		if len(in) > 0 {
			if hasArgs = in[0].Kind() == reflect.Map; hasArgs {
				in = in[1:]
			} else if len(f.Arguments) > 0 {
				if len(in) == 0 {
					return nil, fmt.Errorf("must have parameter for field arguments")
				}
				var err error
				argsPacker, err = b.packerBuilder.MakeStructPacker(f.Arguments, in[0])
				if err != nil {
					return nil, err
				}
				in = in[1:]
			}
		} else if len(f.Arguments) > 0 {
			return nil, fmt.Errorf("must have parameter for field arguments")
		}

		if len(in) > 0 {
			return nil, fmt.Errorf("too many parameters")
		}

		maxNumOfReturns := 2
		if m.Type.NumOut() < maxNumOfReturns-1 {
			return nil, fmt.Errorf("too few return values")
		}

		if m.Type.NumOut() > maxNumOfReturns {
			return nil, fmt.Errorf("too many return values")
		}

		hasError = m.Type.NumOut() == maxNumOfReturns
		if hasError {
			if m.Type.Out(maxNumOfReturns-1) != errorType {
				return nil, fmt.Errorf(`must have "error" as its last return value`)
			}
		}
	}

	fe := &Field{
		FieldDefinition:    *f,
		TypeName:           typeName,
		MethodIndex:        methodIndex,
		FieldIndex:         fieldIndex,
		HasContext:         hasContext,
		HasFieldDefinition: hasFieldDefinition,
		HasPathSegment:     hasPathSegment,
		HasSelectedFields:  hasSelectedFields,
		HasArgs:            hasArgs,
		ArgsPacker:         argsPacker,
		HasError:           hasError,
		TraceLabel:         fmt.Sprintf("GraphQL field: %s.%s", typeName, f.Name),
		Resolver:           resolver,
	}

	var out reflect.Type
	if methodIndex != -1 {
		out = m.Type.Out(0)
		sub, ok := b.schema.EntryPoints["subscription"]
		if ok && typeName == sub.TypeName() && out.Kind() == reflect.Chan {
			out = m.Type.Out(0).Elem()
		}
	} else {
		out = sf.Type
	}
	if err := b.assignExec(fe, &fe.ValueExec, f.Type, out); err != nil {
		return nil, err
	}

	return fe, nil
}

func findMethod(t reflect.Type, name string) int {
	for i := 0; i < t.NumMethod(); i++ {
		if strings.EqualFold(stripUnderscore(name), stripUnderscore(t.Method(i).Name)) {
			return i
		}
	}
	return -1
}

func findField(t reflect.Type, name string, index []int) []int {
	if t.Kind() != reflect.Struct {
		return []int{}
	}

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)

		if field.Type.Kind() == reflect.Struct && field.Anonymous {
			newIndex := findField(field.Type, name, []int{i})
			if len(newIndex) > 1 {
				return append(index, newIndex...)
			}
		}

		if strings.EqualFold(stripUnderscore(name), stripUnderscore(field.Name)) {
			return append(index, i)
		}
	}

	return index
}

// fieldCount helps resolve ambiguity when more than one embedded struct contains fields with the same name.
func fieldCount(t reflect.Type, count map[string]int) map[string]int {
	if t.Kind() != reflect.Struct {
		return nil
	}

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		fieldName := strings.ToLower(stripUnderscore(field.Name))

		if field.Type.Kind() == reflect.Struct && field.Anonymous {
			count = fieldCount(field.Type, count)
		} else {
			if _, ok := count[fieldName]; !ok {
				count[fieldName] = 0
			}
			count[fieldName]++
		}
	}

	return count
}

func unwrapNonNull(t types.Type) (types.Type, bool) {
	if nn, ok := t.(*types.NonNull); ok {
		return nn.OfType, true
	}
	return t, false
}

func stripUnderscore(s string) string {
	return strings.Replace(s, "_", "", -1)
}

func unwrapPtr(t reflect.Type) reflect.Type {
	if t.Kind() == reflect.Ptr {
		return t.Elem()
	}
	return t
}
