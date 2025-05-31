document.addEventListener('DOMContentLoaded', function() {
    const controls = document.getElementById('controls');
    const statusDiv = document.getElementById('status');
    const logsDiv = document.getElementById('logs');
    let configSelect;

    function updateLogs() {
        fetch('/api/relay/logs')
            .then(r => r.json())
            .then(data => {
                if (data.logs && data.logs.length) {
                    logsDiv.innerHTML = data.logs.map(l => l.replace(/</g,'&lt;')).join('<br>');
                } else {
                    logsDiv.innerHTML = '<i>No logs yet.</i>';
                }
            });
    }

    function updateStatus() {
        fetch('/api/status')
            .then(r => r.json())
            .then(data => {
                let outputs = '';
                if (data.output_urls && data.output_urls.length) {
                    outputs = `<br><b>Outputs:</b><ul style='margin:0 0 0 20px;padding:0;'>` +
                        data.output_urls.map(url => `<li>${url}</li>`).join('') + '</ul>';
                }
                let cmds = '';
                if (data.last_cmds && data.last_cmds.length) {
                    cmds = `<br><b>Last Cmds:</b><ul style='margin:0 0 0 20px;padding:0;'>` +
                        data.last_cmds.map(cmd => `<li><code>${cmd}</code></li>`).join('') + '</ul>';
                }
                statusDiv.innerHTML = `<b>Status:</b> ${data.message}` +
                    (data.input_url ? `<br><b>Input:</b> ${data.input_url}` : '') +
                    outputs + cmds;
                // Button logic: Only Start enabled if not running, else Update/Stop enabled
                document.getElementById('startBtn').disabled = data.running;
                document.getElementById('updateBtn').disabled = !data.running;
                document.getElementById('stopBtn').disabled = !data.running;
            });
    }

    function setupSSE() {
        if (!window.EventSource) return false;
        const es = new EventSource('/api/relay/events');
        es.addEventListener('status', function(e) {
            const data = JSON.parse(e.data);
            let outputs = '';
            if (data.output_urls && data.output_urls.length) {
                outputs = `<br><b>Outputs:</b><ul style='margin:0 0 0 20px;padding:0;'>` +
                    data.output_urls.map(url => `<li>${url}</li>`).join('') + '</ul>';
            }
            let cmds = '';
            if (data.last_cmds && data.last_cmds.length) {
                cmds = `<br><b>Last Cmds:</b><ul style='margin:0 0 0 20px;padding:0;'>` +
                    data.last_cmds.map(cmd => `<li><code>${cmd}</code></li>`).join('') + '</ul>';
            }
            statusDiv.innerHTML = `<b>Status:</b> ${data.status === 'running' ? 'Stream running' : 'Idle'}` +
                (data.input_url ? `<br><b>Input:</b> ${data.input_url}` : '') +
                outputs + cmds;
            document.getElementById('startBtn').disabled = data.status === 'running';
            document.getElementById('updateBtn').disabled = data.status !== 'running';
            document.getElementById('stopBtn').disabled = data.status !== 'running';
        });
        es.addEventListener('logs', function(e) {
            const logs = JSON.parse(e.data);
            if (logs && logs.length) {
                logsDiv.innerHTML = logs.map(l => l.replace(/</g,'&lt;')).join('<br>');
            } else {
                logsDiv.innerHTML = '<i>No logs yet.</i>';
            }
        });
        es.onerror = function() {
            es.close();
            // fallback to polling
            setInterval(updateLogs, 2000);
            setInterval(updateStatus, 2000);
        };
        return true;
    }

    if (!setupSSE()) {
        setInterval(updateLogs, 2000);
        setInterval(updateStatus, 2000);
    }

    function loadConfigList() {
        fetch('/api/list-configs')
            .then(r => r.json())
            .then(names => {
                configSelect.innerHTML = '<option value="">-- Select Saved Config --</option>' +
                    names.map(n => `<option value="${n}">${n}</option>`).join('');
            });
    }

    function loadConfigByName(name) {
        if (!name) return;
        fetch('/api/load-config?name=' + encodeURIComponent(name))
            .then(r => r.json())
            .then(cfg => {
                if (cfg.input_url) document.getElementById('inputUrl').value = cfg.input_url;
                if (cfg.output_urls) document.getElementById('outputUrls').value = Array.isArray(cfg.output_urls) ? cfg.output_urls.join('\n') : cfg.output_urls;
            });
    }

    controls.innerHTML = `
        <form id="relayForm" enctype="multipart/form-data">
            <label>RTMP Input URL: <input type="text" id="inputUrl" required placeholder="rtmp://source/live/stream" style="width:300px"></label><br>
            <label>RTMP Output URLs: <textarea id="outputUrls" required placeholder="rtmp://dest/live/stream1\nrtmp://dest/live/stream2" style="width:300px;height:48px;"></textarea></label><br>
            <select id="configSelect"></select>
            <button id="loadBtn" type="button">Load Config</button>
            <button id="deleteBtn" type="button">Delete Config</button>
            <input id="saveName" type="text" placeholder="Config name" style="width:150px" />
            <button id="saveBtn" type="button">Save Config</button>
            <button id="cleanBtn" type="button">Clean Empty/Invalid Configs</button><br>
            <button id="exportBtn" type="button">Export Configs</button>
            <input id="importFile" type="file" accept="application/json" style="display:none" />
            <button id="importBtn" type="button">Import Configs</button><br>
            <button id="startBtn" type="submit">Start Relay</button>
            <button id="updateBtn" type="button">Update Relay</button>
            <button id="stopBtn" type="button">Stop Relay</button>
            <button id="refreshBtn" type="button">Show Status</button>
        </form>
    `;

    configSelect = document.getElementById('configSelect');
    loadConfigList();

    document.getElementById('relayForm').onsubmit = function(e) {
        e.preventDefault();
        const outputUrls = document.getElementById('outputUrls').value
            .split(/\n|,/)
            .map(s => s.trim())
            .filter(Boolean);
        fetch('/api/start', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                input_url: document.getElementById('inputUrl').value,
                output_urls: outputUrls
            })
        }).then(() => updateStatus());
    };
    document.getElementById('stopBtn').onclick = function() {
        fetch('/api/stop', {method: 'POST'}).then(() => updateStatus());
    };
    document.getElementById('refreshBtn').onclick = updateStatus;
    document.getElementById('saveBtn').onclick = function() {
        const name = document.getElementById('saveName').value.trim();
        if (!name) { alert('Enter a config name!'); return; }
        const outputUrls = document.getElementById('outputUrls').value
            .split(/\n|,/)
            .map(s => s.trim())
            .filter(Boolean);
        fetch('/api/save-config', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                name,
                input_url: document.getElementById('inputUrl').value,
                output_urls: outputUrls
            })
        }).then(() => { alert('Config saved!'); loadConfigList(); });
    };
    document.getElementById('loadBtn').onclick = function() {
        loadConfigByName(configSelect.value);
    };
    document.getElementById('deleteBtn').onclick = function() {
        const name = configSelect.value;
        if (!name) { alert('Select a config to delete!'); return; }
        if (!confirm('Delete config "' + name + '"?')) return;
        fetch('/api/delete-config?name=' + encodeURIComponent(name), {method: 'POST'})
            .then(() => { alert('Config deleted!'); loadConfigList(); });
    };
    document.getElementById('cleanBtn').onclick = function() {
        fetch('/api/clean-configs', {method: 'POST'})
            .then(() => { alert('Cleaned empty/invalid configs!'); loadConfigList(); });
    };
    document.getElementById('exportBtn').onclick = function() {
        window.location = '/api/export-configs';
    };
    document.getElementById('importBtn').onclick = function() {
        document.getElementById('importFile').click();
    };
    document.getElementById('importFile').onchange = function(e) {
        const file = e.target.files[0];
        if (!file) return;
        const formData = new FormData();
        formData.append('file', file);
        fetch('/api/import-configs', {
            method: 'POST',
            body: formData
        }).then(() => { alert('Configs imported!'); loadConfigList(); });
    };
    configSelect.onchange = function() {
        loadConfigByName(configSelect.value);
    };

    document.getElementById('updateBtn').onclick = function() {
        const outputUrls = document.getElementById('outputUrls').value
            .split(/\n|,/)
            .map(s => s.trim())
            .filter(Boolean);
        fetch('/api/update', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                input_url: document.getElementById('inputUrl').value,
                output_urls: outputUrls
            })
        })
        .then(() => updateStatus());
    };

    updateStatus();
});
