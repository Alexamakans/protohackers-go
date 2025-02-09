package main

import (
	"log"
	"os"
	"strconv"

	"github.com/Alexamakans/protohackers-go/internal/linereversal"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatalln("Usage: protohackers-go <problem-index>")
	}
	problem, err := strconv.Atoi(os.Args[1])
	if err != nil {
		log.Fatalln("<problem-index> must be a non-negative integer")
	}
	run(problem)
}

func run(problem int) {
	switch problem {
	case 7:
		linereversal.Run()
	default:
		log.Fatalf("Problem %d is not implemented", problem)
	}
}
