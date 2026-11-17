package cloud

import "encoding/json"

const inventoryPageSize = 100
const maxInventoryPages = 1000

// A short page is not evidence of completeness. In particular, incomplete VM
// inventory must never become a proof that every other issuer host is stopped.
type inventoryProgress struct {
	total *int
}

func (p *inventoryProgress) page(body []byte, offset, length int) (bool, error) {
	var envelope struct {
		Count  *int            `json:"count"`
		Result json.RawMessage `json:"result"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Count == nil ||
		*envelope.Count < 0 || *envelope.Count > inventoryPageSize*maxInventoryPages ||
		length < 0 || length > inventoryPageSize || offset < 0 {
		return false, malformed("inventory count")
	}
	if p.total == nil {
		total := *envelope.Count
		p.total = &total
	}
	if *p.total != *envelope.Count || offset+length > *p.total {
		return false, malformed("inventory count changed")
	}
	if len(envelope.Result) == 0 || string(envelope.Result) == "null" {
		if *p.total != 0 || offset != 0 || length != 0 {
			return false, malformed("inventory result missing")
		}
	}
	if offset+length == *p.total {
		return true, nil
	}
	if length != inventoryPageSize {
		return false, malformed("inventory page incomplete")
	}
	return false, nil
}
