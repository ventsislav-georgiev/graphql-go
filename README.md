# graphql-go [![Sourcegraph](https://sourcegraph.com/github.com/graph-gophers/graphql-go/-/badge.svg)](https://sourcegraph.com/github.com/graph-gophers/graphql-go?badge) [![Build Status](https://semaphoreci.com/api/v1/graph-gophers/graphql-go/branches/master/badge.svg)](https://semaphoreci.com/graph-gophers/graphql-go) [![GoDoc](https://godoc.org/github.com/graph-gophers/graphql-go?status.svg)](https://godoc.org/github.com/graph-gophers/graphql-go)

<p align="center"><img src="docs/img/logo.png" width="300"></p>

The goal of this project is to provide full support of the [GraphQL draft specification](https://facebook.github.io/graphql/draft) with a set of idiomatic, easy to use Go packages.

While still under heavy development (`internal` APIs are almost certainly subject to change), this library is
safe for production use.

## Features

- minimal API
- support for `context.Context`
- support for the `OpenTracing` standard
- schema type-checking against resolvers
- resolvers are matched to the schema based on method sets (can resolve a GraphQL schema with a Go interface or Go struct).
- handles panics in resolvers
- parallel execution of resolvers
- subscriptions
   - [sample WS transport](https://github.com/graph-gophers/graphql-transport-ws)

## Roadmap

We're trying out the GitHub Project feature to manage `graphql-go`'s [development roadmap](https://github.com/graph-gophers/graphql-go/projects/1).
Feedback is welcome and appreciated.

## (Some) Documentation

### Basic Sample

```go
package main

import (
        "log"
        "net/http"

        graphql "github.com/graph-gophers/graphql-go"
        "github.com/graph-gophers/graphql-go/relay"
)

type query struct{}

func (_ *query) Hello() string { return "Hello, world!" }

func main() {
        s := `
                type Query {
                        hello: String!
                }
        `
        schema := graphql.MustParseSchema(s, &query{})
        http.Handle("/query", &relay.Handler{Schema: schema})
        log.Fatal(http.ListenAndServe(":8080", nil))
}
```

To test:
```sh
$ curl -XPOST -d '{"query": "{ hello }"}' localhost:8080/query
```

### Resolvers

A resolver must have one method or field for each field of the GraphQL type it resolves. The method or field name has to be [exported](https://golang.org/ref/spec#Exported_identifiers) and match the schema's field's name in a non-case-sensitive way.
You can use struct fields as resolvers by using `SchemaOpt: UseFieldResolvers()`. For example,
```
opts := []graphql.SchemaOpt{graphql.UseFieldResolvers()}
schema := graphql.MustParseSchema(s, &query{}, opts...)
```   

When using `UseFieldResolvers` schema option, a struct field will be used *only* when:
- there is no method for a struct field
- a struct field does not implement an interface method
- a struct field does not have arguments

The method has up to two arguments:

- Optional arguments:
  * `context.Context`
  * `types.FieldDefinition` Meta for the GraphQL field that is being resolved.
  * `types.PathSegment` An recursive structure holding all the resolvers i.e. for `book.author.name` when passed to the `name` field resolver it will contain the resolvers for `author` and `book` also.
  * `types.SelectedFields` Meta for the GraphQL selected fields of a type.
- Mandatory arguments if the corresponding GraphQL field has arguments:
  * `*struct { ... }` The names of the struct fields have to be [exported](https://golang.org/ref/spec#Exported_identifiers) and have to match the names of the GraphQL arguments in a non-case-sensitive way.
or
  * `map[string]interface{}` Provides a non-type checked way of receiving all passed arguments to the GraphQL field.

The method has up to two results:

- The GraphQL field's value as determined by the resolver.
- Optional `error` result.

Example for a simple resolver method:

```go
func (r *helloWorldResolver) Hello() string {
	return "Hello world!"
}
```

The following signature is also allowed:

```go
func (r *helloWorldResolver) Hello(ctx context.Context) (string, error) {
	return "Hello world!", nil
}
```

And with all available arguments:
```go
func (r *helloWorldResolver) Hello(ctx context.Context, fieldDefinition types.FieldDefinition, pathSegment types.PathSegment, args map[string]interface{}) (string, error) {
	return "Hello world!", nil
}
```

> The type checked args struct can be used in place of the map.

### Schema Options

- `UseStringDescriptions()` enables the usage of double quoted and triple quoted. When this is not enabled, comments are parsed as descriptions instead.
- `UseFieldResolvers()` specifies whether to use struct field resolvers.
- `UseDefaultResolvers(defaultResolversProvider types.DefaultResolversProvider)` allows for passing a struct for providing a field resolver dynamically. It requires the struct to have a `GetResolver(fieldDefinition types.FieldDefinition) *types.ResolverInfo` method which will be invoked for each field and return a types.ResolverInfo which will be used for resolving the field.
- `UseDynamicResolvers()` specifies whether to allow the resolvers to return a generic struct for resolving the fields. Once enabled the resolvers can return a `types.DynamicResolver` struct. Usinng this struct for resolving skips the type checking of the return result.
- `MaxDepth(n int)` specifies the maximum field nesting depth in a query. The default is 0 which disables max depth checking.
- `MaxParallelism(n int)` specifies the maximum number of resolvers per request allowed to run in parallel. The default is 10.
- `Tracer(tracer trace.Tracer)` is used to trace queries and fields. It defaults to `trace.OpenTracingTracer`.
- `ValidationTracer(tracer trace.ValidationTracer)` is used to trace validation errors. It defaults to `trace.NoopValidationTracer`.
- `Logger(logger log.Logger)` is used to log panics during query execution. It defaults to `exec.DefaultLogger`.
- `DisableIntrospection()` disables introspection queries.

### Custom Errors

Errors returned by resolvers can include custom extensions by implementing the `ResolverError` interface:

```go
type ResolverError interface {
	error
	Extensions() map[string]interface{}
}
```

Example of a simple custom error:

```go
type droidNotFoundError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e droidNotFoundError) Error() string {
	return fmt.Sprintf("error [%s]: %s", e.Code, e.Message)
}

func (e droidNotFoundError) Extensions() map[string]interface{} {
	return map[string]interface{}{
		"code":    e.Code,
		"message": e.Message,
	}
}
```

Which could produce a GraphQL error such as:

```go
{
  "errors": [
    {
      "message": "error [NotFound]: This is not the droid you are looking for",
      "path": [
        "droid"
      ],
      "extensions": {
        "code": "NotFound",
        "message": "This is not the droid you are looking for"
      }
    }
  ],
  "data": null
}
```

### [Examples](https://github.com/graph-gophers/graphql-go/wiki/Examples)

### [Companies that use this library](https://github.com/graph-gophers/graphql-go/wiki/Users)
