package server

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"

	"github.com/tendermint/tendermint/libs/log"
)

// RegisterRPCFuncs adds a route to mux for each non-websocket function in the
// funcMap, and also a root JSON-RPC POST handler.
func RegisterRPCFuncs(mux *http.ServeMux, funcMap map[string]*RPCFunc, logger log.Logger) {
	for name, fn := range funcMap {
		if fn.ws {
			continue // skip websocket endpoints, not usable via GET calls
		}
		mux.HandleFunc("/"+name, makeHTTPHandler(fn, logger))
	}

	// Endpoints for POST.
	mux.HandleFunc("/", handleInvalidJSONRPCPaths(makeJSONRPCHandler(funcMap, logger)))
}

// Function introspection

// RPCFunc contains the introspected type information for a function.
type RPCFunc struct {
	f        reflect.Value // underlying rpc function
	param    reflect.Type  // the parameter struct, or nil
	result   reflect.Type  // the non-error result type, or nil
	argNames []string      // name of each argument (for display)
	ws       bool          // websocket only
}

// NewRPCFunc constructs an RPCFunc for f, which must be a function whose type
// signature matches one of these schemes:
//
//     func(context.Context) error
//     func(context.Context) (R, error)
//     func(context.Context, *T) error
//     func(context.Context, *T) (R, error)
//
// for an arbitrary struct type T and type R. NewRPCFunc will panic if f does
// not have one of these forms.
func NewRPCFunc(f interface{}, argNames ...string) *RPCFunc {
	rf, err := newRPCFunc(f)
	if err != nil {
		panic("invalid RPC function: " + err.Error())
	}
	return rf
}

// NewWSRPCFunc behaves as NewRPCFunc, but marks the resulting function for use
// via websocket.
func NewWSRPCFunc(f interface{}, argNames ...string) *RPCFunc {
	rf := NewRPCFunc(f, argNames...)
	rf.ws = true
	return rf
}

var (
	ctxType = reflect.TypeOf((*context.Context)(nil)).Elem()
	errType = reflect.TypeOf((*error)(nil)).Elem()
)

// newRPCFunc constructs an RPCFunc for f. See the comment at NewRPCFunc.
func newRPCFunc(f interface{}) (*RPCFunc, error) {
	if f == nil {
		return nil, errors.New("nil function")
	}

	// Check the type and signature of f.
	fv := reflect.ValueOf(f)
	if fv.Kind() != reflect.Func {
		return nil, errors.New("not a function")
	}

	var ptype reflect.Type
	ft := fv.Type()
	if np := ft.NumIn(); np == 0 || np > 2 {
		return nil, errors.New("wrong number of parameters")
	} else if ft.In(0) != ctxType {
		return nil, errors.New("first parameter is not context.Context")
	} else if np == 2 {
		ptype = ft.In(1)
		if ptype.Kind() != reflect.Ptr {
			return nil, errors.New("parameter type is not a pointer")
		}
		ptype = ptype.Elem()
		if ptype.Kind() != reflect.Struct {
			return nil, errors.New("parameter type is not a struct")
		}
	}

	var rtype reflect.Type
	if no := ft.NumOut(); no < 1 || no > 2 {
		return nil, errors.New("wrong number of results")
	} else if ft.Out(no-1) != errType {
		return nil, errors.New("last result is not error")
	} else if no == 2 {
		rtype = ft.Out(0)
	}

	var argNames []string
	if ptype != nil {
		for i := 0; i < ptype.NumField(); i++ {
			name := strings.SplitN(ptype.Field(i).Tag.Get("json"), ",", 2)[0]
			if name != "" {
				argNames = append(argNames, name)
			}
		}
	}

	return &RPCFunc{
		f:        fv,
		param:    ptype,
		result:   rtype,
		argNames: argNames,
	}, nil
}

//-------------------------------------------------------------

// NOTE: assume returns is result struct and error. If error is not nil, return it
func unreflectResult(returns []reflect.Value) (interface{}, error) {
	errV := returns[1]
	if err, ok := errV.Interface().(error); ok && err != nil {
		return nil, err
	}
	rv := returns[0]
	// the result is a registered interface,
	// we need a pointer to it so we can marshal with type byte
	rvp := reflect.New(rv.Type())
	rvp.Elem().Set(rv)
	return rvp.Interface(), nil
}
