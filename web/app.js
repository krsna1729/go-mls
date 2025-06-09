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

    // --- Dynamic Preset Loading ---
    let loadedPresets = {};
    function populatePresetDropdown(presets) {
        const presetSelect = document.getElementById('platformPreset');
        presetSelect.innerHTML = '<option value="">None (Custom/Default)</option>';
        Object.keys(presets).forEach(name => {
            presetSelect.innerHTML += `<option value="${name}">${name}</option>`;
        });
        loadedPresets = presets;
    }
    fetch('/api/relay/presets').then(r => r.json()).then(populatePresetDropdown);

    // --- Move Preset/Options UI to just above Start Relay button ---
    const addRelayRow = relayControls.querySelector('.md-input-row');
    const advancedRow = document.createElement('div');
    advancedRow.className = 'md-input-row';
    advancedRow.innerHTML = `
        <label for="platformPreset" style="margin-right:8px;">Platform Preset:</label>
        <select id="platformPreset" style="margin-right:16px;"></select>
        <label for="videoCodec">Video Codec:</label>
        <input type="text" id="videoCodec" placeholder="e.g. libx264" style="width:90px;">
        <label for="audioCodec">Audio Codec:</label>
        <input type="text" id="audioCodec" placeholder="e.g. aac" style="width:70px;">
        <label for="resolution">Res:</label>
        <input type="text" id="resolution" placeholder="e.g. 1280x720" style="width:80px;">
        <label for="framerate">FPS:</label>
        <input type="text" id="framerate" placeholder="e.g. 30" style="width:40px;">
        <label for="bitrate">Bitrate:</label>
        <input type="text" id="bitrate" placeholder="e.g. 2500k" style="width:60px;">
        <label for="rotation">Rotation</label>
        <select id="rotation" style="min-width:220px; margin-right:16px; border:1.5px solid #b6d0f7; border-radius:6px; background:#f7fafd; color:#222; outline:none; transition:border-color 0.2s; box-shadow:0 1px 2px rgba(25,118,210,0.04); font-size:1rem; padding:10px 12px;">
            <option value="">None</option>
            <option value="transpose=1">90° Clockwise</option>
            <option value="transpose=2">90° Counter-Clockwise</option>
            <option value="transpose=0">90° CCW + Flip Vertically</option>
            <option value="transpose=3">90° CW + Flip Vertically</option>
        </select>
    `;
    // Insert advancedRow just before the Start Relay button
    const startBtn = document.getElementById('startRelayBtn');
    startBtn.parentNode.insertBefore(advancedRow, startBtn);

    // --- Preset change handler (now uses loadedPresets) ---
    relayControls.addEventListener('change', function (e) {
        if (e.target && e.target.id === 'platformPreset') {
            const preset = e.target.value;
            if (loadedPresets[preset]) {
                document.getElementById('videoCodec').value = loadedPresets[preset].video_codec || '';
                document.getElementById('audioCodec').value = loadedPresets[preset].audio_codec || '';
                document.getElementById('resolution').value = loadedPresets[preset].resolution || '';
                document.getElementById('framerate').value = loadedPresets[preset].framerate || '';
                document.getElementById('bitrate').value = loadedPresets[preset].bitrate || '';

                // Set rotation dropdown based on transpose value in preset
                let rotationValue = '';

                // Prioritize 'transpose' if it exists and is a simple digit (like '1')
                if ('transpose' in loadedPresets[preset]) {
                    const t = String(loadedPresets[preset].transpose);
                    if (["0", "1", "2", "3"].includes(t)) {
                        rotationValue = `transpose=${t}`;
                    }
                }

                // If 'transpose' wasn't used or didn't exist, check 'rotation'
                // This modified logic handles cases where 'rotation' is a digit OR "transpose=X"
                if (!rotationValue && 'rotation' in loadedPresets[preset]) {
                    const r = String(loadedPresets[preset].rotation);

                    if (r.startsWith('transpose=')) { // Check if it's already in the "transpose=X" format
                        // Extract the digit and validate it
                        const parts = r.split('=');
                        if (parts.length === 2 && ["0", "1", "2", "3"].includes(parts[1])) {
                            rotationValue = r; // Use the full string "transpose=X" directly
                        }
                    } else if (["0", "1", "2", "3"].includes(r)) { // Fallback: if 'rotation' is just a digit (like old 'transpose' format)
                        rotationValue = `transpose=${r}`;
                    }
                }

                document.getElementById('rotation').value = rotationValue;
            } else {
                document.getElementById('videoCodec').value = '';
                document.getElementById('audioCodec').value = '';
                document.getElementById('resolution').value = '';
                document.getElementById('framerate').value = '';
                document.getElementById('bitrate').value = '';
                document.getElementById('rotation').value = ''; // Clear rotation
            }
        }
    });

    // Move search input to appear under 'Active Relays' heading
    const activeRelaysHeading = Array.from(relayControls.querySelectorAll('h2')).find(h2 => h2.textContent.trim() === 'Active Relays');
    const searchRow = document.createElement('div');
    searchRow.className = 'md-input-row';
    searchRow.innerHTML = `
        <input type="text" id="searchBox" placeholder="Search sources or destinations by name or URL" style="width:60%;margin-bottom:1em;">
    `;
    relayControls.insertBefore(searchRow, activeRelaysHeading.nextSibling);

    let lastSearch = '';
    function highlightMatch(text, query) {
        if (!query) return text;
        const re = new RegExp(`(${query.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')})`, 'ig');
        return text.replace(re, '<mark>$1</mark>');
    }

    function filterData(data, query) {
        if (!query) return data;
        const q = query.toLowerCase();
        const filtered = { ...data, relays: [] };
        for (const relay of data.relays) {
            const inputMatch = (relay.input_name && relay.input_name.toLowerCase().includes(q)) ||
                (relay.input_url && relay.input_url.toLowerCase().includes(q));
            const endpoints = relay.endpoints.filter(ep =>
                (ep.output_name && ep.output_name.toLowerCase().includes(q)) ||
                (ep.output_url && ep.output_url.toLowerCase().includes(q))
            );
            if (inputMatch || endpoints.length > 0) {
                // If input matches, show all endpoints; else only matching endpoints
                filtered.relays.push({
                    ...relay,
                    endpoints: inputMatch ? relay.endpoints : endpoints
                });
            }
        }
        return filtered;
    }

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
        // Use backend-provided status string
        const status = ep.status || 'Stopped';
        const isRunning = status === 'Running';
        return `<tr style="${inputGroupBorder} background:${outputBg};">
            ${i === 0 ? `<td rowspan="${endpointsLen}" style="word-break:break-all; color:#1976d2; font-weight:bold; vertical-align:middle; padding:8px 12px; background:${inputBg}; border:none;" data-label="Input">
                <span class="centered-cell" title="${input}"><span>${ep.input_name || ''}</span><button class='eyeBtn' data-url="${input}" title="Show Input URL"><span class="material-icons">visibility</span></button></span>
            </td>` : ''}
            <td style="word-break:break-all; padding:8px 12px;" data-label="Output">
                <span class="centered-cell" title="${ep.output_url}"><span>${ep.output_name || ep.output_url}</span><button class='eyeBtn' data-url="${ep.output_url}" title="Show Output URL"><span class="material-icons">visibility</span></button></span>
            </td>
            <td style="padding:8px 12px;" data-label="Status">${getStatusBadge(status)}</td>
            <td style="padding:8px 12px;" data-label="Bitrate (kbps)">${isRunning && ep.bitrate ? ep.bitrate : '-'}</td>
            <td style="padding:8px 12px;" data-label="CPU">${isRunning && typeof ep.cpu === 'number' ? ep.cpu.toFixed(1) : '-'}</td>
            <td style="padding:8px 12px;" data-label="Mem">${isRunning && ep.mem ? (ep.mem / (1024 * 1024)).toFixed(1) : '-'}</td>
            <td style="padding:8px 12px;" data-label="Action">
                ${isRunning
                ? `<button class="stopRelayBtn" data-input="${input}" data-output="${ep.output_url}" data-input-name="${ep.input_name || ''}" data-output-name="${ep.output_name || ''}"><span class="material-icons">stop</span>Stop</button>`
                : `<button class="startRelayBtn" data-input="${input}" data-output="${ep.output_url}" data-input-name="${ep.input_name || ''}" data-output-name="${ep.output_name || ''}"><span class="material-icons">play_arrow</span>Start</button>`}
            </td>
        </tr>`;
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

    function addInputHighlightHandlers() {
        // Remove previous listeners if any
        document.querySelectorAll('.input-group-row').forEach(cell => {
            cell.onmouseenter = null;
            cell.onmouseleave = null;
        });
        document.querySelectorAll('tr[data-input-group]').forEach(row => {
            const group = row.getAttribute('data-input-group');
            row.onmouseenter = function () {
                const inputCell = document.querySelector(`.input-group-row[data-input-group="${group}"]`);
                if (inputCell) inputCell.classList.add('input-highlight');
            };
            row.onmouseleave = function () {
                const inputCell = document.querySelector(`.input-group-row[data-input-group="${group}"]`);
                if (inputCell) inputCell.classList.remove('input-highlight');
            };
        });
    }

    function updateUI(data) {
        const searchVal = document.getElementById('searchBox').value.trim();
        lastSearch = searchVal;
        const filtered = filterData(data, searchVal);
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
        let serverHtml = `
  <div class="stats-card">
    <div class="stats-grid stats-grid-custom">
      <div class="stat-block">
        <div class="stat-label">Relay Groups</div>
        <div class="stat-value">${relayGroups}</div>
      </div>
      <div class="stat-block">
        <div class="stat-label">Endpoints</div>
        <div class="stat-value">${totalEndpoints}</div>
      </div>
      <div class="stat-block stat-health">
        <div class="stat-label">Health</div>
        <div class="stat-value">${healthBadge}</div>
      </div>
      <div class="stat-block">
        <div class="stat-label">App CPU</div>
        <div class="stat-value">${appCpu}</div>
      </div>
      <div class="stat-block">
        <div class="stat-label">App Mem</div>
        <div class="stat-value">${appMem}</div>
      </div>
      <div class="stat-block">
        <div class="stat-label">Total CPU</div>
        <div class="stat-value">${totalCpuStr}</div>
      </div>
      <div class="stat-block">
        <div class="stat-label">Total Mem</div>
        <div class="stat-value">${totalMemStr}</div>
      </div>
      <div class="stat-block">
        <div class="stat-label">Total Bitrate</div>
        <div class="stat-value">${Math.round(totalBitrate)} kbps</div>
      </div>
    </div>
  </div>`;
        document.getElementById('serverStats').innerHTML = serverHtml;

        // Relays table
        let html = '';
        if (!filtered || !filtered.relays || filtered.relays.length === 0) {
            html += '<i>No relays running.</i>';
        } else {
            // Sort relays by input_name (fallback input_url)
            filtered.relays.sort((a, b) => {
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
                        <th style="text-align:left; padding:8px 12px;">CPU (%)</th>
                        <th style="text-align:left; padding:8px 12px;">Mem (MB)</th>
                        <th style="text-align:left; padding:8px 12px;">Action</th>
                    </tr>
                </thead>
                <tbody>`;
            for (let relayIdx = 0; relayIdx < filtered.relays.length; relayIdx++) {
                const relay = filtered.relays[relayIdx];
                const input = relay.input_url;
                const inputName = relay.input_name || '';
                const groupId = `group-${relayIdx}`;
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
                    html += `<tr data-input-group="${groupId}">
                        <td class="input-group-row" data-input-group="${groupId}" style="word-break:break-all; color:#1976d2; font-weight:bold; padding:8px 12px; background:${inputBg};">${highlightMatch(inputName, searchVal)}</td>
                        <td colspan="5" style="padding:8px 12px; background:#fff;"><i>No endpoints</i></td>
                    </tr>`;
                } else {
                    for (let i = 0; i < endpoints.length; i++) {
                        endpoints[i].input_name = inputName;
                        if (endpoints[i].bitrate && !isNaN(endpoints[i].bitrate)) {
                            endpoints[i].bitrate = Math.round(Number(endpoints[i].bitrate));
                        }
                        // Use backend status string for running logic
                        const status = endpoints[i].status || 'Stopped';
                        const isRunning = status === 'Running';
                        html += `<tr data-input-group="${groupId}">`;
                        if (i === 0) {
                            html += `<td class="input-group-row" data-input-group="${groupId}" rowspan="${endpoints.length}" style="word-break:break-all; color:#1976d2; font-weight:bold; vertical-align:middle; padding:8px 12px; background:${inputBg}; border:none;" data-label="Input">
                                <span class="centered-cell" title="${input}"><span>${highlightMatch(endpoints[i].input_name || '', searchVal)}</span><button class='eyeBtn' data-url="${input}" title="Show Input URL"><span class="material-icons">visibility</span></button></span>
                            </td>`;
                        }
                        html += `<td style="word-break:break-all; padding:8px 12px;" data-label="Output">
                                <span class="centered-cell" title="${endpoints[i].output_url}"><span>${highlightMatch(endpoints[i].output_name || endpoints[i].output_url, searchVal)}</span><button class='eyeBtn' data-url="${endpoints[i].output_url}" title="Show Output URL"><span class="material-icons">visibility</span></button></span>
                            </td>
                            <td style="padding:8px 12px;" data-label="Status">${getStatusBadge(status)}</td>
                            <td style="padding:8px 12px;" data-label="Bitrate (kbps)">${isRunning && endpoints[i].bitrate ? endpoints[i].bitrate : '-'}</td>
                            <td style="padding:8px 12px;" data-label="CPU">${isRunning && typeof endpoints[i].cpu === 'number' ? endpoints[i].cpu.toFixed(1) : '-'}</td>
                            <td style="padding:8px 12px;" data-label="Mem">${isRunning && endpoints[i].mem ? (endpoints[i].mem / (1024 * 1024)).toFixed(1) : '-'}</td>
                            <td style="padding:8px 12px;" data-label="Action">
                                ${isRunning
                                ? `<button class="stopRelayBtn" data-input="${input}" data-output="${endpoints[i].output_url}" data-input-name="${endpoints[i].input_name || ''}" data-output-name="${endpoints[i].output_name || ''}"><span class="material-icons">stop</span>Stop</button>`
                                : `<button class="startRelayBtn" data-input="${input}" data-output="${endpoints[i].output_url}" data-input-name="${endpoints[i].input_name || ''}" data-output-name="${endpoints[i].output_name || ''}"><span class="material-icons">play_arrow</span>Start</button>`
                            }
                            </td>
                        </tr>`;
                    }
                }
            }
            html += '</tbody></table>';
        }
        document.getElementById('relayTable').innerHTML = html;
        attachRelayButtonHandlers();
        addInputHighlightHandlers();

        document.getElementById('startRelayBtn').onclick = function () {
            const inputUrl = document.getElementById('inputUrl').value.trim();
            const outputUrl = document.getElementById('outputUrl').value.trim();
            const inputName = document.getElementById('inputName').value.trim();
            const outputName = document.getElementById('outputName').value.trim();
            const preset = document.getElementById('platformPreset').value;
            const videoCodec = document.getElementById('videoCodec').value.trim();
            const audioCodec = document.getElementById('audioCodec').value.trim();
            const resolution = document.getElementById('resolution').value.trim();
            const framerate = document.getElementById('framerate').value.trim();
            const bitrate = document.getElementById('bitrate').value.trim();
            const rotation = document.getElementById('rotation').value.trim();
            if (!inputUrl || !outputUrl || !inputName || !outputName) { alert('Input/Output URL and Name required'); return; }
            const ffmpeg_options = { video_codec: videoCodec, audio_codec: audioCodec, resolution, framerate, bitrate, rotation };
            if (rotation) {
                ffmpeg_options.rotation = rotation;
            }
            fetch('/api/relay/start', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ input_url: inputUrl, output_url: outputUrl, input_name: inputName, output_name: outputName, platform_preset: preset, ffmpeg_options })
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

    document.getElementById('searchBox').addEventListener('input', function () {
        fetch('/api/relay/status')
            .then(r => r.json())
            .then(data => updateUI(data));
    });

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
