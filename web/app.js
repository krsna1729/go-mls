document.addEventListener('DOMContentLoaded', function () {
    const relayControls = document.getElementById('controls');

    // Render static input controls once
    relayControls.innerHTML = `<h2>Statistics</h2>
        <div id="serverStats"></div>
        <h2>Add Relay Endpoint</h2>
        <div class="md-input-row">
            <input type="text" id="inputName" placeholder="Input Name">
            <input type="text" id="inputUrl" placeholder="Input URL">
            <input type="text" id="outputName" placeholder="Output Name">
            <input type="text" id="outputUrl" placeholder="Output URL">
            <button id="startRelayBtn"><span class="material-icons">play_arrow</span>Start Relay</button>
        </div>
        <div class="md-action-row">
            <button id="exportBtn" class="secondary"><span class="material-icons">file_download</span>Export</button>
            <input id="importFile" type="file" accept="application/json" style="display:none" />
            <button id="importBtn" class="secondary"><span class="material-icons">file_upload</span>Import</button>
        </div>
        <h2>Active Relays</h2>
        <div id="relayTable"></div>`;

    function formatBytes(bytes) {
        if (bytes < 1024) return bytes + ' B';
        if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
        if (bytes < 1024 * 1024 * 1024) return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
        return (bytes / (1024 * 1024 * 1024)).toFixed(1) + ' GB';
    }

    function getStatusBadge(status) {
        if (status === 'Running') return '<span class="badge badge-running">Running</span>';
        if (status === 'Stopped') return '<span class="badge badge-stopped">Stopped</span>';
        if (status === 'Error') return '<span class="badge badge-error">Error</span>';
        return '<span class="badge badge-unknown">Unknown</span>';
    }

    // Track open details rows by relayIdx-endpointIdx
    const openDetails = new Set();

    function renderEndpointRow(input, ep, endpointsLen, i, inputBg, inputGroupBorder, relayIdx, endpointIdx) {
        const outputBg = inputBg;
        const status = ep.status || (ep.running ? 'Running' : 'Stopped');
        return `<tr style="${inputGroupBorder} background:${outputBg};">
            ${i === 0 ? `<td rowspan="${endpointsLen}" style="word-break:break-all; color:#1976d2; font-weight:bold; vertical-align:middle; padding:8px 12px; background:${inputBg}; border:none;">
                <span class="centered-cell" title="${input}"><span>${ep.input_name || ''}</span><button class='eyeBtn' data-url="${input}" title="Show Input URL"><span class="material-icons">visibility</span></button></span>
            </td>` : ''}
            <td style="word-break:break-all; padding:8px 12px;">
                <span class="centered-cell" title="${ep.output_url}"><span>${ep.output_name || ep.output_url}</span><button class='eyeBtn' data-url="${ep.output_url}" title="Show Output URL"><span class="material-icons">visibility</span></button></span>
            </td>
            <td style="padding:8px 12px;">${getStatusBadge(status)}</td>
            <td style="padding:8px 12px;">${ep.bitrate ? ep.bitrate : '-'}</td>
            <td style="padding:8px 12px; text-align:center;">
                <button class="details-btn" data-relay="${relayIdx}" data-endpoint="${endpointIdx}" title="Show details">${openDetails.has(`${relayIdx}-${endpointIdx}`) ? '&#9650;' : '&#9660;'}</button>
            </td>
            <td style="padding:8px 12px;">
                ${ep.running
                ? `<button class="stopRelayBtn" data-input="${input}" data-output="${ep.output_url}" data-input-name="${ep.input_name || ''}" data-output-name="${ep.output_name || ''}"><span class="material-icons">stop</span>Stop</button>`
                : `<button class="startRelayBtn" data-input="${input}" data-output="${ep.output_url}" data-input-name="${ep.input_name || ''}" data-output-name="${ep.output_name || ''}"><span class="material-icons">play_arrow</span>Start</button>`}
            </td>
        </tr>`;
    }

    function renderDetailsRow(ep, relayIdx, endpointIdx) {
        // Set display based on openDetails
        const key = `${relayIdx}-${endpointIdx}`;
        const display = openDetails.has(key) ? '' : 'none';
        return `<tr class="details-row" style="display:${display};"><td colspan="6" style="padding:10px 2em 10px 3em; font-size:0.98em; color:#333; background:#f9f9fb;">
            <b>CPU:</b> ${typeof ep.cpu === 'number' ? ep.cpu.toFixed(1) + '%' : '-'} &nbsp; 
            <b>Mem:</b> ${ep.mem ? formatBytes(ep.mem) : '-'} &nbsp; 
            <b>Output URL:</b> <span style="word-break:break-all;">${ep.output_url}</span>
        </td></tr>`;
    }

    function attachRelayButtonHandlers() {
        document.querySelectorAll('.stopRelayBtn').forEach(btn => {
            btn.onclick = function () {
                const input = btn.getAttribute('data-input');
                const output = btn.getAttribute('data-output');
                const inputName = btn.getAttribute('data-input-name') || '';
                const outputName = btn.getAttribute('data-output-name') || '';
                fetch('/api/relay/stop', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ input_url: input, output_url: output, input_name: inputName, output_name: outputName })
                }).then(() => { fetchStatus(); });
            };
        });
        document.querySelectorAll('.startRelayBtn').forEach(btn => {
            btn.onclick = function () {
                const input = btn.getAttribute('data-input');
                const output = btn.getAttribute('data-output');
                const inputName = btn.getAttribute('data-input-name') || '';
                const outputName = btn.getAttribute('data-output-name') || '';
                fetch('/api/relay/start', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ input_url: input, output_url: output, input_name: inputName, output_name: outputName })
                }).then(() => { fetchStatus(); });
            };
        });
        document.querySelectorAll('.eyeBtn').forEach(btn => {
            btn.onclick = function () {
                alert('URL: ' + btn.getAttribute('data-url'));
            };
        });
        // Add ripple effect to all buttons
        document.querySelectorAll('button').forEach(btn => {
            btn.addEventListener('click', function (e) {
                const ripple = document.createElement('span');
                ripple.className = 'ripple';
                btn.appendChild(ripple);
                // Force reflow to enable animation
                void ripple.offsetWidth;
                ripple.classList.add('active');
                setTimeout(() => ripple.remove(), 500);
            });
        });
        // Details expand/collapse
        document.querySelectorAll('.details-btn').forEach(btn => {
            btn.onclick = function () {
                const relayIdx = btn.getAttribute('data-relay');
                const endpointIdx = btn.getAttribute('data-endpoint');
                const key = `${relayIdx}-${endpointIdx}`;
                if (openDetails.has(key)) {
                    openDetails.delete(key);
                } else {
                    openDetails.add(key);
                }
                // Force UI update to reflect the new state
                fetch('/api/relay/status')
                    .then(r => r.json())
                    .then(data => updateUI(data));
            };
        });
    }

    function fetchStatus() {
        fetch('/api/relay/status')
            .then(r => r.json())
            .then(data => updateUI(data));
    }

    function updateUI(data) {
        // Server stats
        let appCpu = '0.0%';
        let appMem = '0';
        let relayGroups = 0;
        let totalEndpoints = 0;
        let totalBitrate = 0;
        let health = 'Good';
        let totalCpu = 0;
        let totalMem = 0;
        if (data && data.server) {
            appCpu = data.server.cpu.toFixed(1) + '%';
            appMem = formatBytes(data.server.mem);
        } else {
            appMem = '0';
        }
        // Calculate relay summary, health, and endpoint resource totals
        if (data && data.relays) {
            relayGroups = data.relays.length;
            data.relays.forEach(relay => {
                if (relay.endpoints && Array.isArray(relay.endpoints)) {
                    totalEndpoints += relay.endpoints.length;
                    relay.endpoints.forEach(ep => {
                        if (ep.bitrate && !isNaN(ep.bitrate)) totalBitrate += Number(ep.bitrate);
                        if (ep.status && ep.status === 'Error') health = 'Warning';
                        if (typeof ep.cpu === 'number' && !isNaN(ep.cpu)) totalCpu += ep.cpu;
                        if (typeof ep.mem === 'number' && !isNaN(ep.mem)) totalMem += ep.mem;
                    });
                }
            });
        }
        let healthBadge = health === 'Good'
            ? '<span class="badge badge-healthy">Good</span>'
            : '<span class="badge badge-warning">Warning</span>';
        let totalCpuStr = totalEndpoints ? totalCpu.toFixed(1) + '%' : '0';
        let totalMemStr = totalEndpoints ? formatBytes(totalMem) : '0';
        let serverHtml = `<b>App CPU:</b> ${appCpu} &nbsp; <b>App Mem:</b> ${appMem}<br>
<b>Relay Groups:</b> ${relayGroups} &nbsp; <b>Endpoints:</b> ${totalEndpoints} &nbsp;<b>Health:</b> ${healthBadge}<br>
<b>Total Endpoint Bitrate:</b> ${Math.round(totalBitrate)} kbps &nbsp; <b>Total Endpoint CPU:</b> ${totalCpuStr} &nbsp; <b>Total Endpoint Mem:</b> ${totalMemStr} <br>`;
        document.getElementById('serverStats').innerHTML = serverHtml;

        // Relays table
        let html = '';
        if (!data || !data.relays || data.relays.length === 0) {
            html += '<i>No relays running.</i>';
        } else {
            // Sort relays by input_name (fallback input_url)
            data.relays.sort((a, b) => {
                const aName = a.input_name || a.input_url || '';
                const bName = b.input_name || b.input_url || '';
                return aName.localeCompare(bName, undefined, { numeric: true, sensitivity: 'base' });
            });
            html += `<table style="width:100%;border-collapse:separate;border-spacing:0;">
                <thead>
                    <tr>
                        <th style="text-align:left; padding:8px 12px;">Input</th>
                        <th style="text-align:left; padding:8px 12px;">Output</th>
                        <th style="text-align:left; padding:8px 12px;">Status</th>
                        <th style="text-align:left; padding:8px 12px;">Bitrate (kbps)</th>
                        <th style="text-align:left; padding:8px 12px;">Details</th>
                        <th style="text-align:left; padding:8px 12px;">Action</th>
                    </tr>
                </thead>
                <tbody>`;
            for (let relayIdx = 0; relayIdx < data.relays.length; relayIdx++) {
                const relay = data.relays[relayIdx];
                const input = relay.input_url;
                const inputName = relay.input_name || '';
                // Sort endpoints by output_name (fallback output_url)
                const endpoints = relay.endpoints
                    ? relay.endpoints.slice().sort((a, b) => {
                        const aName = a.output_name || a.output_url || '';
                        const bName = b.output_name || b.output_url || '';
                        return aName.localeCompare(bName, undefined, { numeric: true, sensitivity: 'base' });
                    })
                    : [];
                const inputBg = relayIdx % 2 === 0 ? '#f7fafd' : '#f0f4fa';
                const inputGroupBorder = 'border-top: 3px solid #b6d0f7;';
                if (endpoints.length === 0) {
                    html += `<tr style="${inputGroupBorder}">
                        <td style="word-break:break-all; color:#1976d2; font-weight:bold; padding:8px 12px; background:${inputBg};">${inputName}</td>
                        <td colspan="5" style="padding:8px 12px; background:#fff;"><i>No endpoints</i></td>
                    </tr>`;
                } else {
                    for (let i = 0; i < endpoints.length; i++) {
                        endpoints[i].input_name = inputName;
                        // Ensure per-endpoint bitrate is integer
                        if (endpoints[i].bitrate && !isNaN(endpoints[i].bitrate)) {
                            endpoints[i].bitrate = Math.round(Number(endpoints[i].bitrate));
                        }
                        html += renderEndpointRow(input, endpoints[i], endpoints.length, i, inputBg, inputGroupBorder, relayIdx, i);
                        html += renderDetailsRow(endpoints[i], relayIdx, i);
                    }
                }
            }
            html += '</tbody></table>';
        }
        document.getElementById('relayTable').innerHTML = html;
        attachRelayButtonHandlers();

        document.getElementById('startRelayBtn').onclick = function () {
            const inputUrl = document.getElementById('inputUrl').value.trim();
            const outputUrl = document.getElementById('outputUrl').value.trim();
            const inputName = document.getElementById('inputName').value.trim();
            const outputName = document.getElementById('outputName').value.trim();
            if (!inputUrl || !outputUrl || !inputName || !outputName) { alert('Input/Output URL and Name required'); return; }
            fetch('/api/relay/start', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ input_url: inputUrl, output_url: outputUrl, input_name: inputName, output_name: outputName })
            }).then(() => { fetchStatus(); });
        };
        document.getElementById('exportBtn').onclick = function () {
            window.location = '/api/relay/export';
        };
        document.getElementById('importBtn').onclick = function () {
            document.getElementById('importFile').click();
        };
        document.getElementById('importFile').onchange = function (e) {
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

// The fallback is only necessary if you are not guaranteed to always have input_name and output_name
// in every relay and endpoint object in the status response. If your backend always provides these
// names (even if they are just copies of the URLs), you can safely remove the fallback and sort
// directly by input_name and output_name:

// Example (if names are always present):
// data.relays.sort((a, b) => a.input_name.localeCompare(a.input_name, undefined, { numeric: true, sensitivity: 'base' }));
// endpoints.slice().sort((a, b) => a.output_name.localeCompare(a.output_name, undefined, { numeric: true, sensitivity: 'base' }));

// If you are not sure, keep the fallback to avoid runtime errors or blank sorting keys.
