package main

import (
	"fmt"

	"github.com/cclehui/server_on_gnet/grpc/protobuf"
	"github.com/golang/protobuf/proto"
)

func main() {

	person := &protobuf.Person{}

	person.Name = "cclehui"

	person.Id = 1

	encoded, _ := proto.Marshal(person)

	fmt.Printf("encoded , %v\n", encoded)

	newPerson := &protobuf.Person{}

	proto.Unmarshal(encoded, newPerson)

	fmt.Printf("decoded , %v\n", newPerson)

}
