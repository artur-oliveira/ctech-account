package utils

import (
	"strings"

	"github.com/gofiber/fiber/v3"
)

func IP(c fiber.Ctx) string {
	raw := c.IP()
	if idx := strings.IndexByte(raw, ','); idx != -1 {
		return strings.TrimSpace(raw[:idx])
	}
	return strings.TrimSpace(raw)
}
