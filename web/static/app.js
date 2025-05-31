document.addEventListener('DOMContentLoaded', function() {
    const relayControls = document.getElementById('controls');
    const statusDiv = document.getElementById('status');
    const logsDiv = document.getElementById('logs');

    // Render static input controls once
    relayControls.innerHTML = `<h2>Add Relay</h2>
        <input type="text" id="inputUrl" placeholder="Input URL" style="width:260px;">
        <input type="text" id="outputUrl" placeholder="Output URL" style="width:260px;">
        <button id="startRelayBtn">Start Relay</button>
        <button id="exportBtn">Export Relays</button>
        <input id="importFile" type="file" accept="application/json" style="display:none" />
        <button id="importBtn">Import Relays</button>
        <h2>Active Relays</h2>
        <div id="relayTable"></div>`;

    function fetchStatus() {
        fetch('/api/relay/status')
            .then(r => r.json())
            .then(data => updateUI(data));
    }

    function updateUI(data) {
        // Only update the relay table, not the input fields
        let html = '';
        if (Object.keys(data).length === 0) {
            html += '<i>No relays running.</i>';
        } else {
            html += '<table style="width:100%;border-collapse:collapse;">';
            html += '<tr><th>Input URL</th><th>Output URL</th><th>Status</th><th>Bitrate (kbps)</th><th>Action</th></tr>';
            for (const key in data) {
                const [input, output] = key.split('|');
                const info = data[key];
                html += `<tr>
                    <td style="word-break:break-all;">${input}</td>
                    <td style="word-break:break-all;">${output}</td>
                    <td>${info.running ? 'Running' : 'Stopped'}</td>
                    <td>${info.bitrate ? info.bitrate : '-'}</td>
                    <td><button class="stopRelayBtn" data-key="${key}" ${!info.running ? 'disabled' : ''}>Stop</button></td>
                </tr>`;
            }
            html += '</table>';
        }
        document.getElementById('relayTable').innerHTML = html;

        // Add event listeners for start/stop
        document.getElementById('startRelayBtn').onclick = function() {
            const inputUrl = document.getElementById('inputUrl').value.trim();
            const outputUrl = document.getElementById('outputUrl').value.trim();
            if (!inputUrl || !outputUrl) { alert('Input and Output URL required'); return; }
            fetch('/api/relay/start', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ input_url: inputUrl, output_url: outputUrl })
            }).then(() => { fetchStatus(); });
        };
        document.querySelectorAll('.stopRelayBtn').forEach(btn => {
            btn.onclick = function() {
                const key = btn.getAttribute('data-key');
                const [input, output] = key.split('|');
                fetch('/api/relay/stop', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ input_url: input, output_url: output })
                }).then(() => { fetchStatus(); });
            };
        });
        document.getElementById('exportBtn').onclick = function() {
            window.location = '/api/relay/export';
        };
        document.getElementById('importBtn').onclick = function() {
            document.getElementById('importFile').click();
        };
        document.getElementById('importFile').onchange = function(e) {
            const file = e.target.files[0];
            if (!file) return;
            const formData = new FormData();
            formData.append('file', file);
            fetch('/api/relay/import', {
                method: 'POST',
                body: formData
            }).then(() => { fetchStatus(); });
        };
    }

    // Initial render
    fetchStatus();
    setInterval(fetchStatus, 5000);
});
