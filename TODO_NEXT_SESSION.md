# TODO: Next Session — Refactoring (Final Phase)

**Status:** 8/9 tasks complete (89%)
**Goal:** 100% completion with refactoring
**Time estimate:** 2-3 hours

---

## Quick Start Checklist

### ✅ Phase 1: Architecture (30 min)
- [ ] Create `internal/mcp/tools/scraper.go`
  - [ ] Define `Scraper` interface
  - [ ] Define `Options` struct
  - [ ] Define `Result` struct
  - [ ] Define `ActionsMetadata` struct
- [ ] Create `internal/mcp/tools/common.go`
  - [ ] Add `ValidateURL()` function
  - [ ] Add `GenerateCacheKey()` function
  - [ ] Add shared utilities

### ✅ Phase 2: HTTPScraper (45 min)
- [ ] Rename `scraper.go` → `http_scraper.go`
- [ ] Implement `Scraper` interface
- [ ] Remove duplicated code
- [ ] Update imports
- [ ] Test compilation

### ✅ Phase 3: ChromeScraper (60 min)
- [ ] Rename `js_tool.go` → `chrome_scraper.go`
- [ ] Implement `Scraper` interface
- [ ] Remove duplicated code
- [ ] Update imports
- [ ] Test compilation
- [ ] Test interactive actions still work

### ✅ Phase 4: UnifiedScraper (30 min)
- [ ] Create `internal/mcp/tools/unified_scraper.go`
- [ ] Implement auto-selection logic
- [ ] Implement fallback logic
- [ ] Add smart site detection (JS vs HTTP)
- [ ] Test auto-selection

### ✅ Phase 5: Integration (15 min)
- [ ] Update `internal/mcp/server.go`
- [ ] Update `registerDefaultTools()`
- [ ] Create scrapers instances
- [ ] Pass to tool constructors
- [ ] Test server starts

### ✅ Phase 6: Testing (30 min)
- [ ] Update `scraper_test.go`
- [ ] Add interface tests
- [ ] Add integration tests
- [ ] Test HTTP scraper
- [ ] Test Chrome scraper
- [ ] Test Unified scraper auto-selection
- [ ] Test fallback logic

### ✅ Phase 7: Documentation (15 min)
- [ ] Update `ROADMAP_NEXT_SESSION.md` (9/9 ✅)
- [ ] Update `SESSION_SUMMARY.md` (final)
- [ ] Update `README.md` if needed
- [ ] Create commit with all changes
- [ ] Push to GitHub

---

## File Changes Summary

### New Files (3):
- `internal/mcp/tools/scraper.go` — Interface + types
- `internal/mcp/tools/common.go` — Shared utilities
- `internal/mcp/tools/unified_scraper.go` — Auto-selection

### Renamed Files (2):
- `scraper.go` → `http_scraper.go`
- `js_tool.go` → `chrome_scraper.go`

### Modified Files (3):
- `internal/mcp/server.go` — Integration
- `internal/mcp/tools/scraper_test.go` — Tests
- `README.md` — Documentation (if needed)

---

## Success Criteria

✅ **Refactoring complete when:**
- All scrapers implement `Scraper` interface
- No code duplication
- `UnifiedScraper` auto-works correctly
- All tests pass
- Server starts without errors
- Interactive actions still work
- Documentation updated

✅ **Project 100% complete when:**
- ROADMAP shows 9/9 tasks ✅
- All code committed
- All code pushed to GitHub
- Documentation updated
- Tests pass

---

## Quick Commands

```bash
# During development
go build ./cmd/server                    # Test compilation
go test ./internal/mcp/tools/...         # Run tests
./server --config config.yaml             # Test server

# Before committing
git status                               # Check changes
git add .                                # Stage all
git diff --cached                        # Review changes
git commit -m "Refactoring: unified scraper interface" # Commit
git push origin master                   # Push to GitHub
```

---

## Key Points to Remember

1. **Interface First:** Create `Scraper` interface before refactoring
2. **Small Steps:** One phase at a time, test after each
3. **Backward Compatible:** Keep existing MCP tool signatures
4. **Test Continuously:** Don't wait until end to test
5. **Document Changes:** Update docs as you go

---

## Expected Challenges

### Likely Issues:
1. **Import cycles** — Keep dependencies clear
2. **Cache key compatibility** — Ensure old keys still work
3. **Tool registration** — MCP tools need exact signatures
4. **Browser pool** — ChromeScraper needs proper setup

### Solutions:
1. Use dependency injection carefully
2. Keep cache key generation backward compatible
3. Keep tool signatures unchanged
4. Test browser pool integration early

---

## Final Checklist Before Pushing

- [ ] All tests pass (`go test ./...`)
- [ ] Server starts (`./server --config config.yaml`)
- [ ] HTTP scraping works
- [ ] Chrome scraping works
- [ ] Interactive actions work
- [ ] Auto-selection works
- [ ] Fallback works
- [ ] Documentation updated
- [ ] No compilation warnings
- [ ] Git committed
- [ ] Git pushed

---

**Ready to start! All planning complete.** 🚀

Estimated time: 2-3 hours
Final result: 100% project completion