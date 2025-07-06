class GameOfLifeVisualizer {
    constructor() {
        this.canvas = document.getElementById('gameCanvas');
        this.ctx = this.canvas.getContext('2d');
        this.cellSize = 10;
        this.gridData = null;
        this.topology = null;
        this.lastUpdateTime = Date.now();
        this.isUpdating = false; // Prevent concurrent updates
        
        this.setupEventListeners();
        this.refreshData();
        
        // Auto-refresh every 100ms for very smooth Game of Life visualization
        setInterval(() => {
            this.refreshData();
        }, 100);
        
        // Update timing display every second
        setInterval(() => {
            this.updateTimingDisplay();
        }, 1000);
    }
    
    setupEventListeners() {
        // Control buttons (removed play/pause/step)
        document.getElementById('clearBtn').addEventListener('click', () => this.clearAll());
        document.getElementById('randomBtn').addEventListener('click', () => this.randomizeAll());
        document.getElementById('refreshBtn').addEventListener('click', () => this.refreshData());
        
        // Canvas click handling
        this.canvas.addEventListener('click', (e) => this.handleCanvasClick(e));
    }
    
    async refreshData() {
        try {
            const response = await fetch('/api/grid');
            const data = await response.json();
            
            this.gridData = data.grids;
            this.topology = data.topology;
            this.lastUpdateTime = Date.now();
            
            this.updateStatus();
            this.updateNodeInfo();
            this.draw();
        } catch (error) {
            console.error('Failed to refresh data:', error);
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
        document.getElementById('nodeCount').textContent = nodeCount;
        document.getElementById('regionId').textContent = this.topology.regionId || '-';
        
        // Calculate grid dimensions (assuming 7x7 per node)
        const maxRow = Math.max(...Object.values(this.topology.nodes).map(n => n.position.row)) + 1;
        const maxCol = Math.max(...Object.values(this.topology.nodes).map(n => n.position.col)) + 1;
        
        document.getElementById('gridSize').textContent = `${maxCol * 7} x ${maxRow * 7}`;
        document.getElementById('totalCells').textContent = maxCol * maxRow * 49;
    }
    
    updateNodeInfo() {
        if (!this.topology) return;
        
        const nodeGrid = document.getElementById('nodeGrid');
        nodeGrid.innerHTML = '';
        
        Object.entries(this.topology.nodes).forEach(([position, node]) => {
            const nodeCard = document.createElement('div');
            nodeCard.className = 'node-card';
            
            const gridState = this.gridData[position];
            const generation = gridState ? gridState.generation : 0;
            
            nodeCard.innerHTML = `
                <h4>Node ${node.podId}</h4>
                <p>Position: (${node.position.row}, ${node.position.col})</p>
                <p>Generation: ${generation}</p>
                <p>Endpoint: ${node.endpoint}</p>
            `;
            
            nodeGrid.appendChild(nodeCard);
        });
    }
    
    draw() {
        if (!this.gridData || !this.topology) return;
        
        // Auto-resize canvas based on grid dimensions
        const maxRow = Math.max(...Object.values(this.topology.nodes).map(n => n.position.row)) + 1;
        const maxCol = Math.max(...Object.values(this.topology.nodes).map(n => n.position.col)) + 1;
        
        const canvasWidth = maxCol * 7 * this.cellSize;
        const canvasHeight = maxRow * 7 * this.cellSize;
        
        if (this.canvas.width !== canvasWidth || this.canvas.height !== canvasHeight) {
            this.canvas.width = canvasWidth;
            this.canvas.height = canvasHeight;
        }
        
        this.ctx.clearRect(0, 0, this.canvas.width, this.canvas.height);
        
        Object.entries(this.topology.nodes).forEach(([position, node]) => {
            const gridState = this.gridData[position];
            if (!gridState || !gridState.grid) return;
            
            this.drawNodeGrid(node, gridState.grid);
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
        
        console.log(`Click at canvas (${x.toFixed(1)}, ${y.toFixed(1)}) -> cell (${globalX}, ${globalY})`);
        
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
                    alive: true // Always set to alive for now, could add toggle logic
                })
            });
            
            if (response.ok) {
                console.log(`Successfully updated cell (${globalX}, ${globalY})`);
                // Immediate refresh to show the change
                setTimeout(() => this.refreshData(), 50);
            } else {
                console.error(`Failed to update cell: ${response.status} ${response.statusText}`);
            }
        } catch (error) {
            console.error('Failed to update cell:', error);
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
        // Send randomize command to all nodes
        await this.sendCommandToAllNodes('/randomize');
        // Immediate refresh to show the randomization
        setTimeout(() => this.refreshData(), 100);
    }
    
    async sendCommandToAllNodes(command) {
        if (!this.topology) return;
        
        const promises = Object.values(this.topology.nodes).map(node => {
            return fetch(node.endpoint + command, { method: 'POST' })
                .catch(error => console.error(`Failed to send ${command} to ${node.podId}:`, error));
        });
        
        await Promise.all(promises);
    }
    
    // Removed updateControlButtons - no longer needed without play/pause controls
}

// Initialize the visualizer when the page loads
document.addEventListener('DOMContentLoaded', () => {
    new GameOfLifeVisualizer();
});