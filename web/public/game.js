class GameOfLifeVisualizer {
    constructor() {
        this.canvas = document.getElementById('gameCanvas');
        this.ctx = this.canvas.getContext('2d');
        this.cellSize = 10;
        this.gridData = null;
        this.topology = null;
        this.isPlaying = false;
        
        this.setupEventListeners();
        this.refreshData();
        
        // Auto-refresh every second when playing
        setInterval(() => {
            if (this.isPlaying) {
                this.refreshData();
            }
        }, 1000);
    }
    
    setupEventListeners() {
        // Control buttons
        document.getElementById('playBtn').addEventListener('click', () => this.play());
        document.getElementById('pauseBtn').addEventListener('click', () => this.pause());
        document.getElementById('stepBtn').addEventListener('click', () => this.step());
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
            
            this.updateStatus();
            this.updateNodeInfo();
            this.draw();
        } catch (error) {
            console.error('Failed to refresh data:', error);
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
        const rect = this.canvas.getBoundingClientRect();
        const x = event.clientX - rect.left;
        const y = event.clientY - rect.top;
        
        const globalX = Math.floor(x / this.cellSize);
        const globalY = Math.floor(y / this.cellSize);
        
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
                // Refresh the display after a short delay
                setTimeout(() => this.refreshData(), 100);
            }
        } catch (error) {
            console.error('Failed to update cell:', error);
        }
    }
    
    async play() {
        // Send start command to all nodes
        await this.sendCommandToAllNodes('/start');
        this.isPlaying = true;
        this.updateControlButtons();
    }
    
    async pause() {
        // Send stop command to all nodes
        await this.sendCommandToAllNodes('/stop');
        this.isPlaying = false;
        this.updateControlButtons();
    }
    
    async step() {
        // Send step command to all nodes
        await this.sendCommandToAllNodes('/step');
        setTimeout(() => this.refreshData(), 100);
    }
    
    async clearAll() {
        // For now, just randomize with 0 probability
        // In a full implementation, we'd add a clear endpoint
        console.log('Clear all not implemented yet');
    }
    
    async randomizeAll() {
        // Send randomize command to all nodes
        await this.sendCommandToAllNodes('/randomize');
        setTimeout(() => this.refreshData(), 500);
    }
    
    async sendCommandToAllNodes(command) {
        if (!this.topology) return;
        
        const promises = Object.values(this.topology.nodes).map(node => {
            return fetch(node.endpoint + command, { method: 'POST' })
                .catch(error => console.error(`Failed to send ${command} to ${node.podId}:`, error));
        });
        
        await Promise.all(promises);
    }
    
    updateControlButtons() {
        document.getElementById('playBtn').disabled = this.isPlaying;
        document.getElementById('pauseBtn').disabled = !this.isPlaying;
        document.getElementById('stepBtn').disabled = this.isPlaying;
    }
}

// Initialize the visualizer when the page loads
document.addEventListener('DOMContentLoaded', () => {
    new GameOfLifeVisualizer();
});