// SPDX-License-Identifier: MIT

// Command embed is a Go program that uses GoScheme as a library: it hands a Go
// function to Scheme, calls a Scheme procedure from Go, and reads the result
// back as Go data.
//
//	go run ./examples/embed
//
// The public API lives in the repository's root package, so a Go program
// imports "github.com/MrXie1109/GoScheme" and needs nothing else.
package main

import (
	"fmt"
	"strings"

	goscheme "github.com/MrXie1109/GoScheme"
)

func main() {
	i := goscheme.New()

	// 1. A Go function offered to Scheme.  Looking up the argument with the
	// typed accessor avoids a type switch in the common case, and returning an
	// error raises an ordinary Scheme condition.
	i.Define("host-greeting", 1, 1, func(args []goscheme.Value) (goscheme.Value, error) {
		who, ok := args[0].Str()
		if !ok {
			return goscheme.Value{}, fmt.Errorf("host-greeting: expected a string")
		}
		return goscheme.Str("hello " + who + ", from Go"), nil
	})

	// 2. A Scheme procedure called from Go.
	if _, err := i.Eval("(define (square x) (* x x))"); err != nil {
		panic(err)
	}
	square, _ := i.Lookup("square")
	value, err := i.Call(square, goscheme.Int(12))
	if err != nil {
		panic(err)
	}
	fmt.Println("Go called Scheme and got", value)

	// 3. A script, with what it displays captured rather than printed.
	var printed strings.Builder
	i.SetOutput(&printed)
	result, err := i.Eval(`
	  (import (scheme base) (scheme write))
	  (display (host-greeting "world"))
	  (newline)
	  (list 1 2 3)`)
	if err != nil {
		panic(err)
	}
	fmt.Print("the script printed: ", printed.String())

	// 4. The value of the last form, read back as Go data.
	if items, ok := result.Slice(); ok {
		sum := int64(0)
		for _, item := range items {
			n, _ := item.Int()
			sum += n
		}
		fmt.Printf("the list has %d items summing to %d\n", len(items), sum)
	}

	// 5. A Scheme error is a Go error, and the Scheme side can catch one too.
	if _, err := i.Eval("(car 5)"); err != nil {
		fmt.Println("Scheme error surfaced as:", err)
	}
	caught, err := i.Eval(`(guard (e (#t 'caught-in-scheme)) (car 5))`)
	if err != nil {
		panic(err)
	}
	fmt.Println("and in Scheme it is catchable:", caught)
}
