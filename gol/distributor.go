package gol

import (
	"fmt"
	"time"

	"uk.ac.bris.cs/gameoflife/util"
)

const alive = 255
const dead = 0

func mod(x, m int) int {
	return (x + m) % m
}

func makeMatrix(height, width int) [][]byte {
	matrix := make([][]byte, height)
	for i := range matrix {
		matrix[i] = make([]byte, width)
	}
	return matrix
}

func calculateNeighbours(p Params, x, y int, world [][]byte) int {
	neighbours := 0
	for i := -1; i <= 1; i++ {
		for j := -1; j <= 1; j++ {
			if i != 0 || j != 0 {
				if world[mod(y+i, p.ImageHeight)][mod(x+j, p.ImageWidth)] == alive {
					neighbours++
				}
			}
		}
	}
	return neighbours
}

func calculatePartNextState(p Params, startY, endY int, world [][]byte) [][]byte {

	//make empty part
	height := endY - startY
	newWorldPart := makeMatrix(height, p.ImageWidth)

	//inspect current world, populate new part
	for y, j := startY, 0; y < endY; y, j = y+1, j+1 {
		for x := 0; x < p.ImageWidth; x++ {
			neighbours := calculateNeighbours(p, x, y, world)
			if world[y][x] == alive {
				if neighbours == 2 || neighbours == 3 {
					newWorldPart[j][x] = alive
				} else {
					newWorldPart[j][x] = dead
				}
			} else {
				if neighbours == 3 {
					newWorldPart[j][x] = alive
				} else {
					newWorldPart[j][x] = dead
				}
			}
		}
	}
	return newWorldPart
}

func worker(p Params, startY, endY int, world [][]byte, out chan<- [][]byte) {

	newWorldPart := calculatePartNextState(p, startY, endY, world)
	out <- newWorldPart
}

func sendEvents(c distributorChannels, p Params, world [][]byte, newWorld [][]byte, turn int) {
	for y := 0; y < p.ImageHeight; y++ {
		for x := 0; x < p.ImageWidth; x++ {

			if world[y][x] == alive {
				if newWorld[y][x] == dead {
					c.events <- CellFlipped{CompletedTurns: turn, Cell: util.Cell{X: x, Y: y}}
				}
			} else {
				if newWorld[y][x] == alive {
					c.events <- CellFlipped{CompletedTurns: turn, Cell: util.Cell{X: x, Y: y}}
				}
			}
		}
	}
}

func calculateNextState(c distributorChannels, p Params, world [][]byte, turn int, out []chan [][]byte) [][]byte {

	//make empty world
	newWorld := makeMatrix(0, 0)

	//populate new world with new parts
	for i := 0; i < p.Threads; i++ {
		part := <-out[i]
		newWorld = append(newWorld, part...)
	}

	sendEvents(c, p, world, newWorld, turn)

	return newWorld
}

func calculateAliveCells(p Params, world [][]byte) []util.Cell {
	aliveCells := []util.Cell{}

	for y := 0; y < p.ImageHeight; y++ {
		for x := 0; x < p.ImageWidth; x++ {
			if world[y][x] == alive {
				aliveCells = append(aliveCells, util.Cell{X: x, Y: y})
			}
		}
	}

	return aliveCells
}

func generateOutput(p Params, c distributorChannels, world [][]byte, turn int) {
	//put bytes of final world into outputBytes channel
	for y := 0; y < p.ImageHeight; y++ {
		for x := 0; x < p.ImageWidth; x++ {
			c.outputBytes <- world[y][x]
		}
	}
	fileName := fmt.Sprintf("%vx%vx%v", p.ImageWidth, p.ImageHeight, turn)
	c.ioCommand <- ioOutput
	c.fileName <- fileName
	c.events <- ImageOutputComplete{CompletedTurns: turn, Filename: fileName}
}

type distributorChannels struct {
	events      chan<- Event
	ioCommand   chan<- ioCommand
	ioIdle      <-chan bool
	fileName    chan<- string
	outputBytes chan<- uint8
	inputBytes  <-chan uint8
}

// distributor divides the work between workers and interacts with other goroutines.
func distributor(p Params, c distributorChannels, keyChan <-chan rune) {
	//empty world
	initialWorld := makeMatrix(p.ImageHeight, p.ImageWidth)

	//read input file
	fileName := fmt.Sprintf("%vx%v", p.ImageWidth, p.ImageHeight)
	c.ioCommand <- ioInput
	c.fileName <- fileName
	//read input image bytes to form initial world
	for y := 0; y < p.ImageHeight; y++ {
		for x := 0; x < p.ImageWidth; x++ {
			initialWorld[y][x] = <-c.inputBytes
		}
	}

	// TODO: For all initially alive cells send a CellFlipped Event.
	aliveCells := calculateAliveCells(p, initialWorld)
	for _, cell := range aliveCells {
		c.events <- CellFlipped{CompletedTurns: 0, Cell: cell}
	}

	// TODO: Execute all turns of the Game of Life.
	world := initialWorld

	out := make([]chan [][]byte, p.Threads) //make array of out channels for image parts
	for i := range out {
		out[i] = make(chan [][]byte)
	}

	ticker := time.NewTicker(2 * time.Second)
	done := make(chan bool)

	exit := false

	//distribute workload
	workerHeight := p.ImageHeight / p.Threads
	remainder := p.ImageHeight % p.Threads
	workLoad := make([]int, p.Threads)
	for i := range workLoad {
		workLoad[i] = workerHeight
	}

	for j := 0; j < remainder; j++ {
		workLoad[j]++
	}

	turnTotal := 0
	for turn := 0; turn < p.Turns; turn++ {
		go func() {
			for {
				select {
				case <-done:
					return
				case <-ticker.C:
					numAlive := len(calculateAliveCells(p, world))
					c.events <- AliveCellsCount{CompletedTurns: turn, CellsCount: numAlive}
				}
			}
		}()

		//start workers
		endY := 0
		var startY int
		for i := 0; i < p.Threads; i++ {
			startY = endY
			endY = startY + workLoad[i]
			go worker(p, startY, endY, world, out[i])
		}

		world = calculateNextState(c, p, world, turn, out)
		turnTotal = turn
		c.events <- TurnComplete{CompletedTurns: turn}

		select {
		case keyPress := <-keyChan:
			switch keyPress {
			case 's':
				generateOutput(p, c, world, turn)
			case 'q':
				generateOutput(p, c, world, turn)
				exit = true
			case 'p':
				c.events <- StateChange{CompletedTurns: turn, NewState: Paused}
				fmt.Printf("Current turn: %d\n", turn)
				for {
					keyPress = <-keyChan
					if keyPress == 'p' {
						fmt.Println("Continuing")
						c.events <- StateChange{CompletedTurns: turn, NewState: Executing}
						break
					}
				}
			}
		default:
			break
		}

		if exit { //q pressed
			break
		}

		done <- true

	}

	ticker.Stop()

	aliveCells = calculateAliveCells(p, world)
	c.events <- FinalTurnComplete{CompletedTurns: turnTotal, Alive: aliveCells}

	// Make sure that the Io has finished any output before exiting.
	c.ioCommand <- ioCheckIdle
	<-c.ioIdle

	c.events <- StateChange{turnTotal, Quitting}

	// Close the channel to stop the SDL goroutine gracefully. Removing may cause deadlock.
	close(c.events)
}
