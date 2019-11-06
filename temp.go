package main

import "fmt"

type MyData struct {
	Age int
}

func main() {

	data := MyData{Age: 1}

	fmt.Println("in main 111111, ", data.Age)

	test(data)

	fmt.Println("in main 222222, ", data.Age)
}

func test(data MyData) {

	data.Age = 99

	fmt.Println("in test , ", data.Age)

}
