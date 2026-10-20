package exec

import (
	"light-media/internal/jinja/nodes"
)

type ControlStructure interface {
	nodes.ControlStructure
	Execute(*Renderer, *nodes.ControlStructureBlock) error
}
