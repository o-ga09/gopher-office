package main

import (
	"log"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/o-ga09/gopher-office/internal/client"
)

func main() {
	ebiten.SetWindowTitle("Gopher Office")
	ebiten.SetWindowSize(1280, 720)
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	if err := ebiten.RunGame(client.NewGame()); err != nil {
		log.Fatal(err)
	}
}
