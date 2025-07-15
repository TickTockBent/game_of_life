class GameOfLifeVisualizer {
    constructor() {
        this.canvas = document.getElementById('gameCanvas');
        this.ctx = this.canvas.getContext('2d');
        this.cellSize = 10;
        this.gridData = null;
        this.topology = null;
        this.lastUpdateTime = Date.now();
        this.isUpdating = false; // Prevent concurrent updates
        this.currentInterval = 300; // Track current refresh interval
        this.websocket = null;
        this.reconnectAttempts = 0;
        this.maxReconnectAttempts = 5;
        
        this.setupEventListeners();
        
        // Start WebSocket connection for real-time updates
        this.connectWebSocket();
        
        // Update timing display every second
        setInterval(() => {
            this.updateTimingDisplay();
        }, 1000);
        
        // Update metrics every 500ms for real-time queue monitoring
        setInterval(() => {
            this.updateMetrics();
        }, 500);
    }
    
    setupEventListeners() {
        // Control buttons removed - no longer interactive
        // Canvas click handling removed - no longer interactive
    }
    
    connectWebSocket() {
        const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        const wsUrl = `${protocol}//${window.location.host}/ws`;
        
        console.log(`Connecting to WebSocket: ${wsUrl}`);
        
        try {
            this.websocket = new WebSocket(wsUrl);
            
            this.websocket.onopen = () => {
                console.log('WebSocket connected - real-time updates enabled');
                this.reconnectAttempts = 0;
                document.getElementById('connectionStatus').textContent = 'Connected (WebSocket)';
                document.getElementById('connectionStatus').style.color = 'green';
            };
            
            this.websocket.onmessage = (event) => {
                try {
                    const data = JSON.parse(event.data);
                    this.handleRealtimeUpdate(data);
                } catch (error) {
                    console.error('Failed to parse WebSocket message:', error);
                }
            };
            
            this.websocket.onclose = () => {
                console.log('WebSocket disconnected');
                document.getElementById('connectionStatus').textContent = 'Disconnected';
                document.getElementById('connectionStatus').style.color = 'red';
                this.handleWebSocketReconnect();
            };
            
            this.websocket.onerror = (error) => {
                console.error('WebSocket error:', error);
                document.getElementById('connectionStatus').textContent = 'Error';
                document.getElementById('connectionStatus').style.color = 'red';
            };
            
        } catch (error) {
            console.error('Failed to create WebSocket:', error);
            this.fallbackToPolling();
        }
    }
    
    handleWebSocketReconnect() {
        if (this.reconnectAttempts < this.maxReconnectAttempts) {
            this.reconnectAttempts++;
            const delay = Math.min(1000 * Math.pow(2, this.reconnectAttempts), 30000); // Exponential backoff, max 30s
            console.log(`Reconnecting WebSocket in ${delay}ms (attempt ${this.reconnectAttempts}/${this.maxReconnectAttempts})`);
            
            setTimeout(() => {
                this.connectWebSocket();
            }, delay);
        } else {
            console.log('Max reconnection attempts reached, falling back to polling');
            this.fallbackToPolling();
        }
    }
    
    fallbackToPolling() {
        console.log('Using polling fallback for updates');
        document.getElementById('connectionStatus').textContent = 'Polling Fallback';
        document.getElementById('connectionStatus').style.color = 'orange';
        
        // Start polling as fallback
        this.refreshInterval = setInterval(() => {
            if (!this.isUpdating) {
                this.refreshData();
            }
        }, 1000); // 1 second polling interval
    }
    
    handleRealtimeUpdate(data) {
        if (this.isUpdating) return; // Skip if still processing previous update
        
        this.isUpdating = true;
        try {
            // Process real-time data
            if (data.grids && data.topology) {
                // Merge new data with existing to prevent flickering
                if (this.gridData) {
                    // Keep existing grids if new data is missing them
                    for (const position in this.gridData) {
                        if (!data.grids[position]) {
                            data.grids[position] = this.gridData[position];
                        }
                    }
                }
                
                this.gridData = data.grids;
                this.topology = data.topology;
                this.lastUpdateTime = Date.now();
                
                this.updateStatus();
                this.updateNodeInfo();
                this.draw();
            }
        } catch (error) {
            console.error('Failed to process real-time update:', error);
        } finally {
            this.isUpdating = false;
        }
    }
    
    async refreshData() {
        if (this.isUpdating) return; // Prevent overlapping requests
        
        this.isUpdating = true;
        try {
            const response = await fetch('/api/grid', {
                signal: AbortSignal.timeout(5000) // 5 second timeout
            });
            if (!response.ok) {
                // Handle 503/500 errors gracefully - don't spam logs
                if (response.status >= 500) {
                    console.warn(`Server error ${response.status} - will retry`);
                } else {
                    throw new Error(`HTTP ${response.status}`);
                }
                return; // Skip update, keep current state
            }
            
            const data = await response.json();
            
            // Only update if we got valid data
            if (data.grids && data.topology) {
                // Merge new data with existing to prevent flickering
                if (this.gridData) {
                    // Keep existing grids if new data is missing them
                    for (const position in this.gridData) {
                        if (!data.grids[position]) {
                            data.grids[position] = this.gridData[position];
                        }
                    }
                }
                
                this.gridData = data.grids;
                this.topology = data.topology;
                this.lastUpdateTime = Date.now();
                
                this.updateStatus();
                this.updateNodeInfo();
                this.draw();
            }
        } catch (error) {
            console.error('Failed to refresh data:', error);
        } finally {
            this.isUpdating = false;
        }
    }
    
    
    updateTimingDisplay() {
        const secondsAgo = Math.floor((Date.now() - this.lastUpdateTime) / 1000);
        const lastUpdateElement = document.getElementById('lastUpdate');
        
        if (secondsAgo === 0) {
            lastUpdateElement.textContent = 'Just now';
        } else if (secondsAgo === 1) {
            lastUpdateElement.textContent = '1 second ago';
        } else {
            lastUpdateElement.textContent = `${secondsAgo} seconds ago`;
        }
    }
    
    updateStatus() {
        if (!this.topology) return;
        
        const nodeCount = Object.keys(this.topology.nodes || {}).length;
        
        // Only update if values actually changed
        const nodeCountEl = document.getElementById('nodeCount');
        if (nodeCountEl.textContent !== nodeCount.toString()) {
            nodeCountEl.textContent = nodeCount;
        }
        
        const regionIdEl = document.getElementById('regionId');
        const regionId = this.topology.regionId || '-';
        if (regionIdEl.textContent !== regionId) {
            regionIdEl.textContent = regionId;
        }
        
        // Calculate grid dimensions (assuming 7x7 per node)
        const maxRow = Math.max(...Object.values(this.topology.nodes).map(n => n.position.row)) + 1;
        const maxCol = Math.max(...Object.values(this.topology.nodes).map(n => n.position.col)) + 1;
        
        document.getElementById('gridSize').textContent = `${maxCol * 7} x ${maxRow * 7}`;
        document.getElementById('totalCells').textContent = maxCol * maxRow * 49;
    }
    
    updateNodeInfo() {
        if (!this.topology) return;
        
        const nodeGrid = document.getElementById('nodeGrid');
        
        // Check if topology changed - only rebuild if nodes changed
        const currentNodeIds = Object.keys(this.topology.nodes).sort().join(',');
        if (this.lastNodeIds !== currentNodeIds) {
            // Full rebuild needed
            nodeGrid.innerHTML = '';
            
            Object.entries(this.topology.nodes).forEach(([position, node]) => {
                const nodeCard = document.createElement('div');
                nodeCard.className = 'node-card';
                nodeCard.id = `node-${position}`;
                
                const gridState = this.gridData[position];
                const generation = gridState ? gridState.generation : 0;
                
                nodeCard.innerHTML = `
                    <h4>Node ${node.podId}</h4>
                    <p>Position: (${node.position.row}, ${node.position.col})</p>
                    <p class="generation">Generation: ${generation}</p>
                `;
                
                nodeGrid.appendChild(nodeCard);
            });
            
            this.lastNodeIds = currentNodeIds;
        } else {
            // Just update generation numbers
            Object.entries(this.topology.nodes).forEach(([position, node]) => {
                const nodeCard = document.getElementById(`node-${position}`);
                if (nodeCard) {
                    const gridState = this.gridData[position];
                    const generation = gridState ? gridState.generation : 0;
                    const genElement = nodeCard.querySelector('.generation');
                    if (genElement) {
                        genElement.textContent = `Generation: ${generation}`;
                    }
                }
            });
        }
    }
    
    draw() {
        if (!this.gridData || !this.topology) return;
        
        // Auto-resize canvas based on grid dimensions
        const maxRow = Math.max(...Object.values(this.topology.nodes).map(n => n.position.row)) + 1;
        const maxCol = Math.max(...Object.values(this.topology.nodes).map(n => n.position.col)) + 1;
        
        const canvasWidth = maxCol * 7 * this.cellSize;
        const canvasHeight = maxRow * 7 * this.cellSize;
        
        // Only resize if dimensions actually changed
        if (this.canvas.width !== canvasWidth || this.canvas.height !== canvasHeight) {
            this.canvas.width = canvasWidth;
            this.canvas.height = canvasHeight;
            // Full redraw needed after resize
            this.lastGridStates = null;
        }
        
        // Initialize previous state tracking
        if (!this.lastGridStates) {
            this.lastGridStates = {};
            // Full clear and redraw on first run
            this.ctx.clearRect(0, 0, this.canvas.width, this.canvas.height);
            this.drawAllGrids();
        } else {
            // Incremental updates only
            this.drawChangedGrids();
        }
        
        // Always draw borders on top
        this.drawAllBorders();
        
        // Update saved state
        this.lastGridStates = JSON.parse(JSON.stringify(this.gridData));
    }
    
    drawAllGrids() {
        Object.entries(this.topology.nodes).forEach(([position, node]) => {
            const gridState = this.gridData[position];
            if (gridState && gridState.grid) {
                this.drawNodeGrid(node, gridState.grid);
            }
        });
    }
    
    drawChangedGrids() {
        Object.entries(this.topology.nodes).forEach(([position, node]) => {
            const gridState = this.gridData[position];
            const lastGridState = this.lastGridStates[position];
            
            // Skip if no current data (keep previous rendering)
            if (!gridState || !gridState.grid) return;
            
            // Check if this grid changed
            const hasChanged = !lastGridState || 
                              !lastGridState.grid ||
                              gridState.generation !== lastGridState.generation ||
                              JSON.stringify(gridState.grid) !== JSON.stringify(lastGridState.grid);
            
            if (hasChanged) {
                // Clear only this grid's area
                const startX = node.position.col * 7 * this.cellSize;
                const startY = node.position.row * 7 * this.cellSize;
                this.ctx.clearRect(startX, startY, 7 * this.cellSize, 7 * this.cellSize);
                
                // Redraw this grid
                this.drawNodeGrid(node, gridState.grid);
            }
        });
    }
    
    drawAllBorders() {
        Object.entries(this.topology.nodes).forEach(([position, node]) => {
            this.drawNodeBorder(node);
        });
    }
    
    drawNodeGrid(node, grid) {
        const startX = node.position.col * 7 * this.cellSize;
        const startY = node.position.row * 7 * this.cellSize;
        
        for (let row = 0; row < 7; row++) {
            for (let col = 0; col < 7; col++) {
                const x = startX + col * this.cellSize;
                const y = startY + row * this.cellSize;
                
                if (grid[row] && grid[row][col]) {
                    this.ctx.fillStyle = '#000';
                    this.ctx.fillRect(x, y, this.cellSize - 1, this.cellSize - 1);
                } else {
                    this.ctx.fillStyle = '#fff';
                    this.ctx.fillRect(x, y, this.cellSize - 1, this.cellSize - 1);
                }
            }
        }
    }
    
    drawNodeBorder(node) {
        const startX = node.position.col * 7 * this.cellSize;
        const startY = node.position.row * 7 * this.cellSize;
        const width = 7 * this.cellSize;
        const height = 7 * this.cellSize;
        
        this.ctx.strokeStyle = '#007bff';
        this.ctx.lineWidth = 2;
        this.ctx.strokeRect(startX, startY, width, height);
        
        // Draw node label
        this.ctx.fillStyle = '#007bff';
        this.ctx.font = '12px Arial';
        this.ctx.fillText(
            node.podId, 
            startX + 5, 
            startY + 15
        );
    }
    
    async handleCanvasClick(event) {
        if (this.isUpdating) {
            return; // Prevent rapid clicks from interfering
        }
        
        const rect = this.canvas.getBoundingClientRect();
        const scaleX = this.canvas.width / rect.width;   // Account for CSS scaling
        const scaleY = this.canvas.height / rect.height;
        
        const x = (event.clientX - rect.left) * scaleX;
        const y = (event.clientY - rect.top) * scaleY;
        
        const globalX = Math.floor(x / this.cellSize);
        const globalY = Math.floor(y / this.cellSize);
        
        const gridX = Math.floor(globalX / 7);
        const gridY = Math.floor(globalY / 7);
        
        console.log(`Click at canvas (${x.toFixed(1)}, ${y.toFixed(1)}) -> cell (${globalX}, ${globalY}) -> randomizing grid (${gridX}, ${gridY})`);
        
        this.isUpdating = true;
        
        try {
            const response = await fetch('/api/click', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify({
                    globalX: globalX,
                    globalY: globalY,
                    alive: true // Not used anymore, just randomizes the grid
                })
            });
            
            if (response.ok) {
                console.log(`Successfully randomized grid (${gridX}, ${gridY})`);
                // Immediate refresh to show the change
                setTimeout(() => this.refreshData(), 50);
            } else {
                console.error(`Failed to randomize grid: ${response.status} ${response.statusText}`);
            }
        } catch (error) {
            console.error('Failed to randomize grid:', error);
        } finally {
            // Allow new clicks after a short delay
            setTimeout(() => {
                this.isUpdating = false;
            }, 200);
        }
    }
    
    // Removed play/pause/step methods - simulation runs continuously
    
    async clearAll() {
        // For now, just randomize with 0 probability
        // In a full implementation, we'd add a clear endpoint
        console.log('Clear all not implemented yet');
    }
    
    async randomizeAll() {
        console.log('Randomizing all nodes...');
        try {
            const response = await fetch('/api/randomize', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                }
            });
            
            if (response.ok) {
                const result = await response.json();
                console.log(`Randomized ${result.success}/${result.total} nodes successfully`);
                // Immediate refresh to show the randomization
                setTimeout(() => this.refreshData(), 100);
            } else {
                console.error(`Failed to randomize: ${response.status} ${response.statusText}`);
            }
        } catch (error) {
            console.error('Failed to randomize all nodes:', error);
        }
    }
    
    async sendCommandToAllNodes(command) {
        if (!this.topology) return;
        
        const promises = Object.values(this.topology.nodes).map(node => {
            return fetch(node.endpoint + command, { method: 'POST' })
                .catch(error => console.error(`Failed to send ${command} to ${node.podId}:`, error));
        });
        
        await Promise.all(promises);
    }
    
    async updateMetrics() {
        try {
            const response = await fetch('/api/metrics', {
                signal: AbortSignal.timeout(3000) // 3 second timeout for metrics
            });
            
            if (!response.ok) {
                // Handle server errors gracefully for metrics
                if (response.status >= 500) {
                    console.warn(`Metrics server error ${response.status} - skipping update`);
                    return;
                }
                throw new Error(`HTTP ${response.status}`);
            }
            
            const data = await response.json();
            
            if (data && data.controller) {
                const metrics = data.controller;
                // Current generation from controller
                if (metrics.generation !== undefined) {
                    document.getElementById('controllerLoad').textContent = `Gen ${metrics.generation}`;
                }
                
                // Registration count  
                if (metrics.nodes !== undefined) {
                    document.getElementById('forceSteps').textContent = `${metrics.nodes} nodes`;
                    document.getElementById('nodeReady').textContent = `${metrics.nodes} ready`;
                }
                
                // Active grids count
                if (metrics.activeGrids !== undefined) {
                    document.getElementById('reregistrations').textContent = `${metrics.activeGrids} active`;
                }
                
                // Timestamp of last update
                if (metrics.timestamp) {
                    const now = Math.floor(Date.now() / 1000);
                    const age = now - metrics.timestamp;
                    document.getElementById('lastStep').textContent = `${age}s ago`;
                }
                
                // Queue metrics
                const totalQueueSize = metrics.totalQueueSize || 0;
                
                // Update total queue size
                const queueEl = document.getElementById('queueSize');
                if (queueEl) {
                    queueEl.textContent = totalQueueSize;
                    // Color-code based on total queue size
                    if (totalQueueSize > 500) {
                        queueEl.style.color = 'red';
                    } else if (totalQueueSize > 100) {
                        queueEl.style.color = 'orange';
                    } else {
                        queueEl.style.color = 'green';
                    }
                }
                
                // Update individual queue sizes
                if (metrics.regQueueSize !== undefined) {
                    document.getElementById('registerQueue').textContent = metrics.regQueueSize || 0;
                }
                if (metrics.stateQueueSize !== undefined) {
                    document.getElementById('stateQueue').textContent = metrics.stateQueueSize || 0;
                }
                if (metrics.haloQueueSize !== undefined) {
                    document.getElementById('haloQueue').textContent = metrics.haloQueueSize || 0;
                }
                if (metrics.webQueueSize !== undefined) {
                    document.getElementById('webQueue').textContent = Math.max(0, metrics.webQueueSize);
                }
                if (metrics.stepQueueSize !== undefined) {
                    document.getElementById('stepQueue').textContent = metrics.stepQueueSize || 0;
                }
            }
        } catch (error) {
            console.error('Failed to fetch metrics:', error);
        }
    }
    
    // Removed updateControlButtons - no longer needed without play/pause controls
}

// Initialize the visualizer when the page loads
document.addEventListener('DOMContentLoaded', () => {
    new GameOfLifeVisualizer();
});