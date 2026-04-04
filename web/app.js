document.addEventListener('DOMContentLoaded', function () {
    // Build the UI
    const container = document.getElementById('app');
    container.innerHTML = `
        <div class="stats-card">
            <h3>Server Statistics</h3>
            <div id="serverStats" class="stats-grid"></div>
        </div>
        
        <div class="section">
            <h3>Inputs <button id="addInputBtn" class="btn btn-primary">+ Add Input</button></h3>
            <div id="inputsList"></div>
        </div>
        
        <div class="section">
            <h3>Outputs</h3>
            <div id="outputsList"></div>
        </div>
        
        <div class="modal" id="inputModal">
            <div class="modal-content">
                <span class="close" id="closeInputModal">&times;</span>
                <h3>Add Input</h3>
                <form id="inputForm">
                    <div class="form-group">
                        <label>Stream Name:</label>
                        <input type="text" id="inputName" required placeholder="e.g., my-stream">
                    </div>
                    <div class="form-group">
                        <label>Source URL (optional - leave empty for push):</label>
                        <input type="text" id="inputUrl" placeholder="rtmp://source/live or leave empty">
                    </div>
                    <button type="submit" class="btn btn-primary">Create Input</button>
                </form>
            </div>
        </div>
        
        <div class="modal" id="outputModal">
            <div class="modal-content">
                <span class="close" id="closeOutputModal">&times;</span>
                <h3>Add Output</h3>
                <form id="outputForm">
                    <div class="form-group">
                        <label>Stream:</label>
                        <select id="outputStream" required></select>
                    </div>
                    <div class="form-group">
                        <label>Output ID:</label>
                        <input type="text" id="outputId" required placeholder="e.g., youtube">
                    </div>
                    <div class="form-group">
                        <label>Remote URL:</label>
                        <input type="text" id="outputUrl" required placeholder="rtmp://dest/live/key">
                    </div>
                    <button type="submit" class="btn btn-primary">Create Output</button>
                </form>
            </div>
        </div>
    `;

    // Stats polling
    async function fetchStats() {
        try {
            const data = await API.getStats();
            updateStats(data);
            updateInputs(data.inputs || []);
            updateOutputs(data.outputs || []);
        } catch (err) {
            console.error('Failed to fetch stats:', err);
        }
    }

    function updateStats(data) {
        const statsDiv = document.getElementById('serverStats');
        const cpu = data.server?.cpu?.toFixed(1) || '0';
        const mem = data.server?.mem_mb?.toFixed(1) || '0';
        const inputs = data.inputs?.length || 0;
        const outputs = data.outputs?.length || 0;
        
        statsDiv.innerHTML = `
            <div class="stat-block"><div class="stat-label">Inputs</div><div class="stat-value">${inputs}</div></div>
            <div class="stat-block"><div class="stat-label">Outputs</div><div class="stat-value">${outputs}</div></div>
            <div class="stat-block"><div class="stat-label">CPU</div><div class="stat-value">${cpu}%</div></div>
            <div class="stat-block"><div class="stat-label">Memory</div><div class="stat-value">${mem} MB</div></div>
        `;
    }

    function updateInputs(inputs) {
        const listDiv = document.getElementById('inputsList');
        if (inputs.length === 0) {
            listDiv.innerHTML = '<p class="empty-state">No inputs. Create one to start streaming.</p>';
            return;
        }
        
        listDiv.innerHTML = `<table class="data-table">
            <thead><tr><th>Stream Path</th><th>Mode</th><th>Status</th><th>Remote URL</th><th>Actions</th></tr></thead>
            <tbody>
                ${inputs.map(input => `
                    <tr>
                        <td><strong>${input.stream_path}</strong></td>
                        <td>${input.mode}</td>
                        <td><span class="badge badge-${input.status === 'Active' ? 'healthy' : 'warning'}">${input.status}</span></td>
                        <td>${input.remote_url || '-'}</td>
                        <td>
                            <button onclick="deleteInput('${input.stream_path}')" class="btn btn-danger btn-sm">Delete</button>
                            <button onclick="addOutputFor('${input.stream_path}')" class="btn btn-secondary btn-sm">+ Output</button>
                        </td>
                    </tr>
                `).join('')}
            </tbody>
        </table>`;
    }

    function updateOutputs(outputs) {
        const listDiv = document.getElementById('outputsList');
        if (outputs.length === 0) {
            listDiv.innerHTML = '<p class="empty-state">No outputs configured.</p>';
            return;
        }
        
        listDiv.innerHTML = `<table class="data-table">
            <thead><tr><th>Stream</th><th>Output ID</th><th>Remote URL</th><th>Status</th><th>Actions</th></tr></thead>
            <tbody>
                ${outputs.map(output => `
                    <tr>
                        <td>${output.stream_path}</td>
                        <td>${output.output_id}</td>
                        <td>${output.remote_url}</td>
                        <td><span class="badge badge-${output.status === 'Running' ? 'healthy' : 'warning'}">${output.status}</span></td>
                        <td>
                            <button onclick="deleteOutput('${output.stream_path}', '${output.output_id}')" class="btn btn-danger btn-sm">Delete</button>
                        </td>
                    </tr>
                `).join('')}
            </tbody>
        </table>`;
    }

    // Input modal
    const inputModal = document.getElementById('inputModal');
    document.getElementById('addInputBtn').onclick = () => inputModal.style.display = 'block';
    document.getElementById('closeInputModal').onclick = () => inputModal.style.display = 'none';
    
    document.getElementById('inputForm').onsubmit = async (e) => {
        e.preventDefault();
        const streamPath = document.getElementById('inputName').value;
        const remoteUrl = document.getElementById('inputUrl').value;
        
        try {
            await API.createInput(streamPath, remoteUrl || null);
            inputModal.style.display = 'none';
            document.getElementById('inputForm').reset();
            fetchStats();
        } catch (err) {
            alert('Failed to create input: ' + err.message);
        }
    };

    // Output modal
    const outputModal = document.getElementById('outputModal');
    document.getElementById('closeOutputModal').onclick = () => outputModal.style.display = 'none';

    document.getElementById('outputForm').onsubmit = async (e) => {
        e.preventDefault();
        const streamPath = document.getElementById('outputStream').value;
        const outputId = document.getElementById('outputId').value;
        const remoteUrl = document.getElementById('outputUrl').value;
        
        try {
            await API.createOutput(streamPath, outputId, remoteUrl);
            outputModal.style.display = 'none';
            document.getElementById('outputForm').reset();
            fetchStats();
        } catch (err) {
            alert('Failed to create output: ' + err.message);
        }
    };

    // Global functions
    window.deleteInput = async (streamPath) => {
        if (!confirm(`Delete input "${streamPath}"?`)) return;
        try {
            await API.deleteInput(streamPath);
            fetchStats();
        } catch (err) {
            alert('Failed to delete input: ' + err.message);
        }
    };

    window.deleteOutput = async (streamPath, outputId) => {
        if (!confirm(`Delete output "${outputId}"?`)) return;
        try {
            await API.deleteOutput(streamPath, outputId);
            fetchStats();
        } catch (err) {
            alert('Failed to delete output: ' + err.message);
        }
    };

    window.addOutputFor = async (streamPath) => {
        // Populate stream dropdown
        const select = document.getElementById('outputStream');
        select.innerHTML = `<option value="${streamPath}">${streamPath}</option>`;
        document.getElementById('outputId').value = '';
        document.getElementById('outputUrl').value = '';
        outputModal.style.display = 'block';
    };

    // Close modals on outside click
    window.onclick = (e) => {
        if (e.target.classList.contains('modal')) {
            e.target.style.display = 'none';
        }
    };

    // Initial fetch
    fetchStats();
    // Poll every 3 seconds
    setInterval(fetchStats, 3000);
});
