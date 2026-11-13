package priceskeptic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

func (s *server) search(runID int64, item string) {
	s.sem <- struct{}{}
	var err error
	defer func() {
		<-s.sem
		snap, finished := s.runs.finish(runID, item, err)
		if !finished {
			return
		}
		log.Printf("run %d finished: done=%d failed=%d total=%d", snap.ID, snap.Done, snap.Failed, snap.Total)
		go s.notifyResults()
	}()

	err = s.runItem(s.root, item)

	if err != nil && !s.results.HasRows(item) {
		reason := "Search failed: " + shorten(err.Error(), 180)
		if noteErr := s.results.NoOffer(item, reason, nil); noteErr != nil {
			log.Printf("record failed search for %q: %v", item, noteErr)
		}
	}

	if err != nil {
		log.Printf("search %q: %v", item, err)
	}
}

func (s *server) runItem(parent context.Context, item string) error {
	firstCtx, cancel := context.WithTimeout(parent, s.cfg.timeout)
	firstText, sessionID, err := s.execChild(firstCtx, "--file", s.cfg.skill, searchPrompt(item, time.Now().Format("2006-01-02"), s.cfg.postalCode))
	cancel()
	if err != nil {
		if strings.TrimSpace(firstText) == "" {
			return err
		}
		log.Printf("item %q: first pass ended with %v; using the answer it produced", item, err)
	}
	firstRaw := parseOffers(firstText)

	followCtx, cancel := context.WithTimeout(parent, s.cfg.timeout)
	followText, followErr := s.followUp(followCtx, item, sessionID, firstText)
	cancel()
	if followErr != nil {
		log.Printf("item %q: follow-up failed: %v", item, followErr)
	}
	followRaw := parseOffers(followText)

	merged := append(followRaw, firstRaw...)
	offers, dropped, capped := normalizeOffers(item, merged, s.cfg.requireDeal)
	trustCtx, cancelTrust := context.WithTimeout(parent, 30*time.Second)
	s.cfg.trust.Enrich(trustCtx, offers)
	cancelTrust()
	kept, lowTrust := filterByTrust(offers, s.cfg.minTrust)
	written := 0
	if len(kept) == 0 {
		reason := "No offer found that meets the criteria: in stock and genuinely discounted, at a verified low, clearly below trend, or the cheapest verified offer."
		var candidate *offer
		if len(offers) > 0 {
			reason = "Offers were found but dropped by the store trust filter."
		} else if cheap, ok := cheapestCandidate(merged); ok {
			reason += fmt.Sprintf(" Cheapest seen: %s %.0f kr (not discounted).", cheap.Store, offerCost(cheap))
			c := cheap
			candidate = &c
		}
		if err := s.results.NoOffer(item, reason, candidate); err != nil {
			return err
		}
	} else {
		var err error
		written, err = s.results.Replace(item, kept)
		if err != nil {
			return err
		}
	}
	log.Printf("item %q: first=%d follow=%d written=%d dropped=%d capped=%d low_trust=%d", item, len(firstRaw), len(followRaw), written, dropped, capped, lowTrust)
	return nil
}

// hasOffer reports whether text contains at least one offer that survives the
// same basic validation the search pipeline applies.
func hasOffer(text string) bool {
	for _, r := range parseOffers(text) {
		if _, ok := toOffer(r); ok {
			return true
		}
	}
	return false
}

// cheapestCandidate is the cheapest buyable offer the search saw, including
// offers the deal gate rejected. It makes a miss message concrete instead of
// generic.
func cheapestCandidate(raw []rawOffer) (offer, bool) {
	offers := make([]offer, 0, len(raw))
	for _, r := range raw {
		if o, ok := toOffer(r); ok {
			offers = append(offers, o)
		}
	}
	return cheapestOffer(offers)
}

type agentEvent struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionID"`
	Part      struct {
		Text   string `json:"text"`
		Type   string `json:"type"`
		Tool   string `json:"tool"`
		Reason string `json:"reason"`
		State  struct {
			Status string `json:"status"`
			Input  struct {
				URL     string `json:"url"`
				Query   string `json:"query"`
				Command string `json:"command"`
			} `json:"input"`
			Output string `json:"output"`
		} `json:"state"`
	} `json:"part"`
}

func (s *server) followUp(ctx context.Context, item, sessionID, firstText string) (string, error) {
	if sessionID != "" {
		text, _, err := s.execChild(ctx, "--session", sessionID, followUpPrompt(item, "", s.cfg.postalCode))
		return text, err
	}
	text, _, err := s.execChild(ctx, "--file", s.cfg.skill, followUpPrompt(item, firstText, s.cfg.postalCode))
	return text, err
}

func (s *server) execChild(ctx context.Context, extra ...string) (string, string, error) {
	args := []string{"run", "--format", "json"}
	if s.cfg.model != "" {
		args = append(args, "--model", s.cfg.model)
	}
	args = append(args, extra...)

	cmd := exec.CommandContext(ctx, s.cfg.opencode, args...)
	var stdout, stderr bytes.Buffer
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", "", err
	}
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return "", "", err
	}
	reader := bufio.NewReader(pipe)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			stdout.Write(line)
			s.cfg.webLog.Write(webURLs(line))
		}
		if readErr != nil {
			break
		}
	}
	err = cmd.Wait()
	text, sessionID, complete := extractText(stdout.Bytes())
	if err == nil {
		return text, sessionID, nil
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	} else if msg := strings.TrimSpace(stderr.String()); msg != "" {
		err = fmt.Errorf("%w: %s", err, msg)
	}
	// opencode can exit non-zero after the session already produced its final
	// answer, for example when a tool asked a question that a headless run
	// cannot answer. Keep the answer instead of discarding the whole search.
	if !complete && !hasOffer(text) {
		return "", sessionID, err
	}
	return text, sessionID, err
}

// extractText reads opencode's JSON event stream. A child that writes offer
// JSONL directly has no text events, so its raw stdout is the payload.
// complete reports whether a text answer was followed by a successful step
// finish, which means the run produced a usable answer regardless of its exit
// code.
func extractText(raw []byte) (string, string, bool) {
	var parts []string
	var sessionID string
	lastText, lastStop := -1, -1
	dec := json.NewDecoder(bytes.NewReader(raw))
	for i := 0; ; i++ {
		var ev agentEvent
		if err := dec.Decode(&ev); err != nil {
			break
		}
		if sessionID == "" {
			sessionID = ev.SessionID
		}
		if ev.Type == "text" && ev.Part.Text != "" {
			parts = append(parts, ev.Part.Text)
			lastText = i
		}
		if ev.Type == "step_finish" && ev.Part.Reason == "stop" {
			lastStop = i
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, "\n"), sessionID, lastStop > lastText
	}
	return string(raw), sessionID, false
}

func readList(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var items []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		items = append(items, line)
	}
	return items, nil
}

func searchPrompt(item, today, postalCode string) string {
	return fmt.Sprintf(`Read the attached shopping-expert skill and follow it exactly.

Today is %s.

The buyer's Norwegian postal code is %s. Use it for every shipping estimate and prefer home delivery over a pickup point.

Find current salg, tilbud, kampanje, and black offers in Norway, or from stores that ship to Norway, for: %s

Treat any size, capacity, or maximum price limit in the item as a hard filter. Omit offers that do not meet it.

Use the websearch tool and open the product pages to verify price, shipping, stock, rating, and price history. Do not trust search snippets.

Return only JSONL in the skill's schema, one object per line, at most 5 offers for this item, ranked by total landed cost. No prose, no markdown, no code fences. Omit any offer you cannot verify. If there are no valid offers, return nothing.`, today, postalCode, item)
}

func followUpPrompt(item, firstText, postalCode string) string {
	prompt := fmt.Sprintf(`Are there any better current salg, tilbud, kampanje, or black offers than these I found?

The buyer's Norwegian postal code is %s. Use it for every shipping estimate and prefer home delivery over a pickup point.

Re-check salg, tilbud, kampanje, and black offers in Norway, or from stores that ship to Norway, for: %s

Treat any size, capacity, or maximum price limit in the item as a hard filter. Omit offers that do not meet it.

Return only JSONL in the shopping-expert skill's schema, one object per line, at most 5 offers for this item, including the previous offers that are still valid plus any better ones, ranked by total landed cost. No prose, no markdown, no code fences. If there are no valid offers, return nothing.`, postalCode, item)
	if firstText = strings.TrimSpace(firstText); firstText != "" {
		prompt += "\n\nPrevious offers:\n" + firstText
	}
	return prompt
}
