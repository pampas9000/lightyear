package response

import (
	"strconv"

	"github.com/gofiber/fiber/v3"
)

const (
	// DefaultLimit is the page size when a list request gives no limit.
	DefaultLimit = 20
	// MaxLimit is the largest page size a list request may ask for.
	MaxLimit = 100
)

// List is the data of every list response: one page of items, newest first,
// and the total across all pages.
type List[T any] struct {
	Items []T   `json:"items"`
	Page  int   `json:"page"`
	Limit int   `json:"limit"`
	Total int64 `json:"total"`
}

// Page is the page a list request asks for.
type Page struct {
	Page  int
	Limit int
}

// Offset is how many items come before the page.
func (p Page) Offset() int { return (p.Page - 1) * p.Limit }

// NewList builds a list response; items is never null in the JSON.
func NewList[T any](items []T, p Page, total int64) List[T] {
	if items == nil {
		items = []T{}
	}
	return List[T]{Items: items, Page: p.Page, Limit: p.Limit, Total: total}
}

// ParsePage reads ?page (default 1, at least 1) and ?limit (default
// DefaultLimit, 1 to MaxLimit). When either is invalid it answers
// 400 error.param.invalid naming the field and returns ok=false; the handler
// then returns err.
func ParsePage(c fiber.Ctx) (p Page, ok bool, err error) {
	page, perr := strconv.Atoi(c.Query("page", "1"))
	if perr != nil || page < 1 {
		return Page{}, false, RespondErrorWithDetails(c, fiber.StatusBadRequest, CodeParamInvalid, "Invalid page.",
			ValidationDetail{Field: "page", Reason: "must be a positive integer"})
	}
	limit, lerr := strconv.Atoi(c.Query("limit", strconv.Itoa(DefaultLimit)))
	if lerr != nil || limit < 1 || limit > MaxLimit {
		return Page{}, false, RespondErrorWithDetails(c, fiber.StatusBadRequest, CodeParamInvalid, "Invalid limit.",
			ValidationDetail{Field: "limit", Reason: "must be 1-" + strconv.Itoa(MaxLimit)})
	}
	return Page{Page: page, Limit: limit}, true, nil
}
