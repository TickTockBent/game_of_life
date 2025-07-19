# Public Game of Life Engine

Join the distributed Conway's Game of Life! This Docker container allows anyone to contribute a 7x7 grid section to the global simulation running at [ticktockbent.com:8082](http://ticktockbent.com:8082).

## Quick Start

```bash
# Run with default settings
docker run -p 8080:8080 gameoflife-public-engine

# Run with your own display name
docker run -p 8080:8080 -e DISPLAY_NAME="YourName" gameoflife-public-engine

# Run with custom controller (for testing)
docker run -p 8080:8080 \
  -e DISPLAY_NAME="YourName" \
  -e CONTROLLER_URL="http://your-controller:8082" \
  gameoflife-public-engine
```

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `DISPLAY_NAME` | `Anonymous` | Your display name shown in the web interface |
| `CONTROLLER_URL` | `http://ticktockbent.com:8082` | Controller endpoint to connect to |
| `PORT` | `8080` | Port for health checks (must match -p flag) |
| `EXTERNAL_IP` | (auto-detected) | Your external IP address |

## What This Does

1. **Connects to Controller**: Your container registers with the main controller and receives a position in the global 10x10 grid
2. **Computes Game of Life**: Your container calculates Conway's Game of Life rules for your assigned 7x7 section
3. **Shares Border Data**: Patterns can flow seamlessly between your grid and neighboring grids
4. **Synchronized Stepping**: All containers step forward together in perfect synchronization

## Viewing the Global Grid

Visit the web interface to see your grid contributing to the global simulation:
- **Production**: [http://ticktockbent.com:8082](http://ticktockbent.com:8082)
- Your grid section will show your abbreviated node ID
- The "Active Nodes" section will show your full display name

## Technical Details

- **Grid Size**: 7x7 cells per container
- **Update Rate**: ~1 generation per second (barrier synchronized)
- **Max Capacity**: 100 simultaneous containers
- **Network**: HTTP REST API + WebSocket streaming
- **Auto-Recovery**: Automatically re-registers if connection is lost

## Requirements

- Docker installed
- Port 8080 available
- Outbound internet access to controller

## Building from Source

```bash
# Clone the repository
git clone https://github.com/ticktockbent/game_of_life.git
cd game_of_life

# Build the public engine
docker build -t gameoflife-public-engine -f Dockerfile.public-engine .

# Run it
docker run -p 8080:8080 -e DISPLAY_NAME="YourName" gameoflife-public-engine
```

## Troubleshooting

**Connection Issues:**
- Check that port 8080 is available: `lsof -i :8080`
- Verify outbound connectivity: `curl http://ticktockbent.com:8082/health`
- Check container logs: `docker logs <container-id>`

**Registration Failed:**
- Grid may be full (100 containers max)
- Controller may be down for maintenance
- Check your EXTERNAL_IP setting if behind NAT

**Performance Issues:**
- Ensure stable internet connection
- Container needs consistent connectivity for barrier synchronization
- Consider running on a server rather than laptop for 24/7 operation

## Contributing

This is part of the distributed Game of Life project. See the main repository for development details and contribution guidelines.

## License

MIT License - Feel free to run, modify, and distribute!