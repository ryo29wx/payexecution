package main

import (
	"testing" 
	"time"
	"fmt"
	"reflect"
)

func TestTimeToString(t *testing.T) {
	ti := time.Now()
	result := timeToString(ti)
	v := reflect.ValueOf(result)

	fmt.Println(result)
	fmt.Println(v.Type())
}

func TestRequestPayment(t *testing.T) {
	ti := time.Now()
	result := timeToString(ti)
	v := reflect.ValueOf(result)

	fmt.Println(result)
	fmt.Println(v.Type())
}