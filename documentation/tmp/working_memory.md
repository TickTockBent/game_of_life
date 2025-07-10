# Working Memory - Game of Life Project

*Persistent notes across context cleanups and sessions*

## Current Status (2025-07-10)

### Architecture Changes Made
- **Router eliminated** - Simplified architecture by removing router pod entirely
- **Controller externalized** - Running controller as Docker container outside K3s on port 8082
- **Direct port forwarding** - Router forwards external:8082 → host:8082 → Docker container
- **Channel-based controller** - Replaced all locks with message queues (zero locks)

### Current Deployment State
- **Controller**: Docker container on host at `ticktockbent.com:8082` (1-second generation timer)
- **Engines**: 100 pods in K3s, pointing to external controller (200ms minimum step delay)
- **Web**: Scaled to 0, but patched to use external controller URL
- **Networking**: Successfully scales to 100 engines with improved timing!

### Scale Test Results
**10 Engines:**
- ✅ All 10 engines running and registered successfully
- ✅ Zero timeouts across all pods  
- ✅ Response times: 12-41ms (excellent under load)
- ✅ Controller stable: 11 nodes, 5219+ generations processed

**99 Engines (Stress Test):**
- ✅ 99/99 pods running, 100 total nodes registered
- ✅ Controller stable: 6.43% CPU, 7MB RAM, 6050+ generations
- ⚠️ Sporadic timeouts: ~2/99 engines experiencing occasional 3s timeouts
- ✅ Scale limit: External controller handles ~99 engines before hitting connection limits
- ✅ Proves architecture scales remarkably well

### Bugs Discovered
1. **Health checker not cleaning up stale nodes** 
   - Test node `test-pod` still registered after 10+ minutes with no heartbeats
   - Channel-based controller sends health-check messages but doesn't process stale cleanup
   - Need to add stale node removal logic to message processor
   - **Priority**: Fix after current testing phase

### Key Findings
- **K3s networking was the culprit** - External controller eliminates all timeouts
- **Engines work perfectly** with external controller (3-6ms response times vs 3000ms timeouts)
- **Channel architecture works** - No lock contention, clean message processing

### Next Steps
1. Scale up more engines to test multi-engine performance
2. Test web interface with external controller
3. Fix health checker stale node cleanup
4. Consider permanent external deployment strategy

### Performance Notes
- External controller: 3-6ms response times consistently
- K3s internal: Frequent 3000ms timeouts, DNS issues
- Engine catch-up mode working properly (rapid /generation calls when behind)

---
*Last updated: 2025-07-10 12:50*