document.addEventListener('DOMContentLoaded', function () {
    const relayControls = document.getElementById('controls');

    // Render static input controls once
    relayControls.innerHTML = `
        <h2>Statistics</h2>
        <div id="serverStats"></div>
        <h2>Add Relay Endpoint</h2>
        <div class="md-input-row relay-input-grid" id="addRelayRow">
            <input type="text" id="inputName" placeholder="Input Name">
            <input type="text" id="inputUrl" placeholder="Input URL">
            <input type="text" id="outputName" placeholder="Output Name">
            <input type="text" id="outputUrl" placeholder="Output URL">
        </div>
        <div id="advancedOptionsContainer"></div>
        <div class="md-input-row">
            <button id="startRelayBtn"><span class="material-icons">play_arrow</span>Start Relay</button>
        </div>
        <div class="md-action-row">
            <button id="exportBtn" class="secondary"><span class="material-icons">file_download</span>Export</button>
            <input id="importFile" type="file" accept="application/json" style="display:none" />
            <button id="importBtn" class="secondary"><span class="material-icons">file_upload</span>Import</button>
        </div>
        <h2>Active Relays</h2>
        <div class="md-input-row" id="searchRow"></div>
        <div id="relayTable"></div>`;

    // Responsive grid for relay input row
    // (CSS moved to style.css)

    // --- Dynamic Preset Loading ---
    let loadedPresets = {};
    function populatePresetDropdown(presets) {
        const presetSelect = document.getElementById('platformPreset');
        presetSelect.innerHTML = '<option value="">None (Default)</option>';
        Object.keys(presets).forEach(name => {
            presetSelect.innerHTML += `<option value="${name}">${name}</option>`;
        });
        loadedPresets = presets;
    }
    fetch('/api/relay/presets').then(r => r.json()).then(populatePresetDropdown);

    // --- Advanced Options UI ---
    const advancedOptionsContainer = document.getElementById('advancedOptionsContainer');
    const advancedRow = document.createElement('div');
    advancedRow.className = 'md-input-row';
    advancedRow.style.display = 'flex';
    advancedRow.style.flexWrap = 'wrap';
    advancedRow.style.alignItems = 'center';
    advancedRow.style.gap = '12px';
    advancedRow.style.marginBottom = '8px';
    // Responsive styles: collapse advanced options like server stats grid on small screens
    // (CSS moved to style.css)

    // Use same height as Input Name/Input URL (default 38px)
    const inputHeight = '38px';
    const advancedInputWidth = '140px'; // Set a consistent width for all advanced inputs
    const inputStyle = `height:${inputHeight}; min-height:${inputHeight}; box-sizing:border-box; border:1.5px solid #b6d0f7; border-radius:6px; background:#f7fafd; color:#222; outline:none; transition:border-color 0.2s; box-shadow:0 1px 2px rgba(25,118,210,0.04); font-size:1rem; padding:8px 10px; width:${advancedInputWidth}; display:block;`;
    const selectStyle = inputStyle; // Use same width for selects

    // Helper: label and input vertically aligned, label right-aligned, input full width in cell
    function advancedField(labelFor, labelText, inputHtml) {
        return `
            <div style="display:flex; align-items:center; gap:8px;">
                <label for="${labelFor}" style="min-width:110px; text-align:right; display:inline-block; height:${inputHeight}; line-height:${inputHeight}; color:#1976d2; font-weight:500; vertical-align:middle;">${labelText}</label>
                ${inputHtml}
            </div>
        `;
    }

    advancedRow.innerHTML = `
        <div class="advanced-options-group" style="display:flex; flex-direction:column; gap:8px; width:100%; background:rgba(243,247,252,0.7); border:1px solid #e3eaf5; border-radius:7px; padding:10px 14px 6px 14px; margin-bottom:6px;">
            <!-- Platform Preset Row -->
            <div style="display:flex; align-items:center; gap:8px; margin-bottom:2px;">
                <label for="platformPreset" style="min-width:110px; text-align:right; display:inline-block; height:${inputHeight}; line-height:${inputHeight}; color:#1976d2; font-weight:500; vertical-align:middle;">Platform Preset:</label>
                <select id="platformPreset" style="${selectStyle}"></select>
            </div>
            <!-- Options Grid: Responsive columns/rows via CSS -->
            <div class="advanced-options-grid">
                ${advancedField('videoCodec', 'Video Codec:', `<input type="text" id="videoCodec" placeholder="e.g. libx264" style="${inputStyle}">`)}
                ${advancedField('framerate', 'FPS:', `<input type="text" id="framerate" placeholder="e.g. 30" style="${inputStyle}">`)}
                ${advancedField('resolution', 'Resolution:', `<input type="text" id="resolution" placeholder="e.g. 1280x720" style="${inputStyle}">`)}
                ${advancedField('audioCodec', 'Audio Codec:', `<input type="text" id="audioCodec" placeholder="e.g. aac" style="${inputStyle}">`)}
                ${advancedField('bitrate', 'Bitrate:', `<input type="text" id="bitrate" placeholder="e.g. 2500k" style="${inputStyle}">`)}
                ${advancedField('rotation', 'Rotation:', `<select id="rotation" style="${selectStyle}">
                    <option value="">None</option>
                    <option value="transpose=1">90° Clockwise</option>
                    <option value="transpose=2">90° Counter-Clockwise</option>
                    <option value="transpose=0">90° CCW + Flip Vertically</option>
                    <option value="transpose=3">90° CW + Flip Vertically</option>
                </select>`)}
            </div>
        </div>
    `;

    // Remove dynamic style injection for advanced-options-grid (CSS is now in style.css)
    advancedOptionsContainer.appendChild(advancedRow);

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
    const searchRow = document.getElementById('searchRow');
    searchRow.innerHTML = `
        <input type="text" id="searchBox" placeholder="Search sources or destinations by name or URL" style="width:60%;margin-bottom:1em;">
    `;

    let lastSearch = '';
    function highlightMatch(text, query) {
        if (!query) return text;
        const re = new RegExp(`(${query.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')})`, 'ig');
        return text.replace(re, '<mark>$1</mark>');
    }

    function filterData(data, query) {
        if (!query) return data;
        const q = query.toLowerCase();
        // Adapted for new API: data.relays is [{input, outputs}]
        const filtered = { ...data, relays: [] };
        if (!data.relays) return filtered;
        for (const relay of data.relays) {
            const input = relay.input || {};
            const inputMatch = (input.input_name && input.input_name.toLowerCase().includes(q)) ||
                (input.input_url && input.input_url.toLowerCase().includes(q));
            let outputs = relay.outputs || [];
            let matchingOutputs = outputs.filter(out =>
                (out.output_name && out.output_name.toLowerCase().includes(q)) ||
                (out.output_url && out.output_url.toLowerCase().includes(q))
            );
            if (inputMatch || matchingOutputs.length > 0) {
                filtered.relays.push({
                    ...relay,
                    outputs: inputMatch ? outputs : matchingOutputs
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

    function formatBitrate(kbps) {
        if (kbps >= 1000) return (kbps / 1000).toFixed(2) + ' Mbps';
        if (kbps > 0) return Math.round(kbps) + ' kbps';
        return '0 kbps';
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
            <td style="padding:8px 12px;" data-label="Bitrate (kbps)">${isRunning && typeof ep.bitrate === 'number' ? ep.bitrate : '-'}</td>
            <td style="padding:8px 12px;" data-label="CPU">${isRunning && typeof ep.cpu === 'number' ? ep.cpu.toFixed(1) : '-'}</td>
            <td style="padding:8px 12px;" data-label="Mem">${isRunning && typeof ep.mem === 'number' ? (ep.mem / (1024 * 1024)).toFixed(1) : '-'}</td>
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
                    body: JSON.stringify({
                        input_url: input,
                        output_url: output,
                        input_name: inputName,
                        output_name: outputName
                    })
                }).then(() => { fetchStatus(); });
            };
        });
        document.querySelectorAll('.eyeBtn').forEach(btn => {
            btn.onclick = function () {
                alert('URL: ' + btn.getAttribute('data-url'));
            };
        });
        
        // Add delete input button handlers
        document.querySelectorAll('.deleteInputBtn').forEach(btn => {
            btn.onclick = function () {
                const input = btn.getAttribute('data-input');
                const inputName = btn.getAttribute('data-input-name') || '';
                
                if (confirm(`Are you sure you want to delete input "${inputName}" and all its outputs? This action cannot be undone.`)) {
                    fetch('/api/relay/delete-input', {
                        method: 'POST',
                        headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify({
                            input_url: input,
                            input_name: inputName
                        })
                    }).then(response => {
                        if (response.ok) {
                            fetchStatus();
                        } else {
                            response.text().then(text => {
                                alert('Failed to delete input: ' + text);
                            });
                        }
                    }).catch(err => {
                        console.error('Delete input error:', err);
                        alert('Failed to delete input: ' + err.message);
                    });
                }
            };
        });
        
        // Add delete output button handlers
        document.querySelectorAll('.deleteOutputBtn').forEach(btn => {
            btn.onclick = function () {
                const input = btn.getAttribute('data-input');
                const output = btn.getAttribute('data-output');
                const inputName = btn.getAttribute('data-input-name') || '';
                const outputName = btn.getAttribute('data-output-name') || '';
                
                if (confirm(`Are you sure you want to delete output "${outputName}"? This action cannot be undone.`)) {
                    fetch('/api/relay/delete-output', {
                        method: 'POST',
                        headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify({
                            input_url: input,
                            output_url: output,
                            input_name: inputName,
                            output_name: outputName
                        })
                    }).then(response => {
                        if (response.ok) {
                            fetchStatus();
                        } else {
                            response.text().then(text => {
                                alert('Failed to delete output: ' + text);
                            });
                        }
                    }).catch(err => {
                        console.error('Delete output error:', err);
                        alert('Failed to delete output: ' + err.message);
                    });
                }
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

    // Attach handler for the top Start Relay button
    document.getElementById('startRelayBtn').onclick = function () {
        const inputName = document.getElementById('inputName').value.trim();
        const inputUrl = document.getElementById('inputUrl').value.trim();
        const outputName = document.getElementById('outputName').value.trim();
        const outputUrl = document.getElementById('outputUrl').value.trim();
        const platformPreset = document.getElementById('platformPreset').value || '';
        // Advanced options
        const ffmpegOptions = {
            video_codec: document.getElementById('videoCodec').value.trim(),
            audio_codec: document.getElementById('audioCodec').value.trim(),
            resolution: document.getElementById('resolution').value.trim(),
            framerate: document.getElementById('framerate').value.trim(),
            bitrate: document.getElementById('bitrate').value.trim(),
            rotation: document.getElementById('rotation').value.trim()
        };
        fetch('/api/relay/start', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                input_url: inputUrl,
                output_url: outputUrl,
                input_name: inputName,
                output_name: outputName,
                platform_preset: platformPreset,
                ffmpeg_options: ffmpegOptions
            })
        }).then(() => { fetchStatus(); });
    };

    // Update table Start buttons to only send minimal info
    document.querySelectorAll('.startRelayBtn').forEach(btn => {
        btn.onclick = function () {
            const input = btn.getAttribute('data-input');
            const output = btn.getAttribute('data-output');
            const inputName = btn.getAttribute('data-input-name') || '';
            const outputName = btn.getAttribute('data-output-name') || '';
            fetch('/api/relay/start', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    input_url: input,
                    output_url: output,
                    input_name: inputName,
                    output_name: outputName
                })
            }).then(() => { fetchStatus(); });
        };
    });

    // --- Import/Export button handlers ---
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
        }).then(() => { 
            fetchStatus(); 
            alert('Import completed successfully!');
        }).catch(err => {
            console.error('Import failed:', err);
            alert('Import failed. Check console for details.');
        });
    };

    function fetchStatus() {
        fetch('/api/relay/status')
            .then(r => r.json())
            .then(data => updateUI(data));
    }

    function updateUI(data) {
        // Expect data: { server: {cpu, mem}, relays: [...] }
        window.latestRelayStatus = data;
        window.dispatchEvent(new Event('relayStatusUpdated'));
        const searchVal = document.getElementById('searchBox').value.trim();
        const filtered = filterData(data, searchVal);
        let relayGroups = 0, totalEndpoints = 0, totalCpu = 0, totalMem = 0, totalBitrate = 0, health = 'Good';
        let appCpu = '0.0%';
        let appMem = '0';
        if (filtered && filtered.server) {
            appCpu = typeof filtered.server.cpu === 'number' ? filtered.server.cpu.toFixed(1) + '%' : '0.0%';
            appMem = typeof filtered.server.mem === 'number' ? formatBytes(filtered.server.mem) : '0';
        }
        if (filtered && filtered.relays) {
            relayGroups = filtered.relays.length;
            filtered.relays.forEach(relay => {
                if (relay.input) {
                    if (typeof relay.input.cpu === 'number') totalCpu += relay.input.cpu;
                    if (typeof relay.input.mem === 'number') totalMem += relay.input.mem;
                    // Input speed is not included in total bitrate (it's a different metric)
                    if (relay.input.status === 'Error') health = 'Warning';
                }
                if (relay.outputs && Array.isArray(relay.outputs)) {
                    totalEndpoints += relay.outputs.length;
                    relay.outputs.forEach(out => {
                        if (typeof out.cpu === 'number') totalCpu += out.cpu;
                        if (typeof out.mem === 'number') totalMem += out.mem;
                        if (typeof out.bitrate === 'number') totalBitrate += out.bitrate;
                        if (out.status === 'Error') health = 'Warning';
                    });
                }
            });
        }
        let healthBadge = health === 'Good'
            ? '<span class="badge badge-healthy">Good</span>'
            : '<span class="badge badge-warning">Warning</span>';
        let totalCpuStr = (relayGroups + totalEndpoints) ? totalCpu.toFixed(1) + '%' : '0';
        let totalMemStr = (relayGroups + totalEndpoints) ? formatBytes(totalMem) : '0';
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
        <div class="stat-value">${formatBitrate(totalBitrate)}</div>
      </div>
    </div>
  </div>`;
        document.getElementById('serverStats').innerHTML = serverHtml;

        // Render relay table with input/output separation - use filtered data
        let html = '';
        if (!filtered.relays || filtered.relays.length === 0) {
            html += '<i>No relays running.</i>';
        } else {
            // Sort filtered relays by input name, then outputs by output name
            filtered.relays.sort((a, b) => (a.input.input_name || '').localeCompare(b.input.input_name || ''));
            filtered.relays.forEach(relay => {
                if (relay.outputs && Array.isArray(relay.outputs)) {
                    relay.outputs.sort((a, b) => (a.output_name || '').localeCompare(b.output_name || ''));
                }
            });
            // In the relay table header, add an Actions column under Input
            html += `<table class="relay-table" style="width:100%;border-collapse:separate;border-spacing:0; font-size:1rem;">
<thead>
    <tr style="background:#eaf2fb;">
        <th colspan="6" style="text-align:center; padding:6px 8px; border-bottom:1px solid #b6d0f7;">Input</th>
        <th colspan="6" style="text-align:center; padding:6px 8px; border-bottom:1px solid #b6d0f7;">Output</th>
    </tr>
    <tr style="background:#eaf2fb;">
        <th style="text-align:center; padding:6px 4px; width:140px;">Name</th>
        <th style="text-align:center; padding:6px 4px; width:70px;">Status</th>
        <th style="text-align:center; padding:6px 4px; width:60px;">CPU (%)</th>
        <th style="text-align:center; padding:6px 4px; width:70px;">Mem (MB)</th>
        <th style="text-align:center; padding:6px 4px; width:65px;">Speed (x)</th>
        <th style="text-align:center; padding:6px 4px; width:60px;">Actions</th>
        <th style="text-align:center; padding:6px 4px; width:130px;">Name</th>
        <th style="text-align:center; padding:6px 4px; width:70px;">Status</th>
        <th style="text-align:center; padding:6px 4px; width:60px;">CPU (%)</th>
        <th style="text-align:center; padding:6px 4px; width:70px;">Mem (MB)</th>
        <th style="text-align:center; padding:6px 4px; width:90px;">Bitrate (kbps)</th>
        <th style="text-align:center; padding:6px 4px; width:90px;">Actions</th>
    </tr>
</thead><tbody>`;
            filtered.relays.forEach((relay, relayIdx) => {
                const input = relay.input.input_url;
                const inputName = relay.input.input_name || '';
                const inputStatus = relay.input.status || 'Stopped';
                const inputError = relay.input.last_error || '';
                const inputBg = relayIdx % 2 === 0 ? '#f7fafd' : '#f0f4fa';
                
                if (!relay.outputs || relay.outputs.length === 0) {
                    // No outputs - single row with consistent structure
                    // For input rows (no outputs)
                    html += `<tr data-input-group="group-${relayIdx}">
                        <td class="input-group-row" data-input-group="group-${relayIdx}" title="${input}" style="word-break:break-all; color:#1976d2; font-weight:bold; padding:6px 8px; background:${inputBg}; text-align:center;">${inputName}</td>
                        <td style="padding:6px 8px; background:${inputBg}; text-align:center;">${getStatusBadge(inputStatus)}</td>
                        <td style="padding:6px 8px; background:${inputBg}; text-align:center;">${inputStatus === 'Running' && typeof relay.input.cpu === 'number' ? relay.input.cpu.toFixed(1) : '-'}</td>
                        <td style="padding:6px 8px; background:${inputBg}; text-align:center;">${inputStatus === 'Running' && typeof relay.input.mem === 'number' ? Math.round(relay.input.mem / (1024 * 1024)) : '-'}</td>
                        <td style="padding:6px 8px; background:${inputBg}; text-align:center;">${inputStatus === 'Running' && typeof relay.input.speed === 'number' ? relay.input.speed.toFixed(2) + 'x' : '-'}</td>
                        <td style="padding:6px 8px; background:${inputBg}; text-align:center;">
        <button class="deleteInputBtn" data-input="${input}" data-input-name="${inputName}" title="Delete Input" style="padding:4px 6px; min-width:auto; min-height:auto; background:#dc3545; color:white; border:none; border-radius:3px; font-size:0.8em;"><span class="material-icons" style="font-size:16px;">delete</span></button>
    </td>
                        <td style="padding:6px 8px; font-style:italic; color:#999; text-align:center;">${inputError ? `<div style='color:red; font-size:0.85em; margin-top:2px; text-align:center;'>${inputError}</div>` : '<i>No outputs</i>'}</td>
                        <td style="padding:6px 8px; text-align:center;">-</td>
                        <td style="padding:6px 8px; text-align:center;">-</td>
                        <td style="padding:6px 8px; text-align:center;">-</td>
                        <td style="padding:6px 8px; text-align:center;">-</td>
                        <td style="padding:6px 8px; text-align:center;">-</td>
                    </tr>`;
                } else {
                    // Has outputs - create one row per output
                    relay.outputs.forEach((out, i) => {
                        const outputStatus = out.status || 'Stopped';
                        const outputError = out.last_error || '';
                        const isFirstOutput = i === 0;
                        html += `<tr data-input-group="group-${relayIdx}">`;
                        // For input rows with outputs, update the first output row to include the input actions column with rowspan
                        if (isFirstOutput) {
                            html += `<td class="input-group-row" data-input-group="group-${relayIdx}" rowspan="${relay.outputs.length}" title="${input}" style="word-break:break-all; color:#1976d2; font-weight:bold; vertical-align:middle; padding:6px 8px; background:${inputBg}; border:none; text-align:center;">${inputName}</td>`;
                            html += `<td rowspan="${relay.outputs.length}" style="padding:6px 8px; background:${inputBg}; vertical-align:middle; text-align:center;">${getStatusBadge(inputStatus)}</td>`;
                            html += `<td rowspan="${relay.outputs.length}" style="padding:6px 8px; background:${inputBg}; vertical-align:middle; text-align:center;">${inputStatus === 'Running' && typeof relay.input.cpu === 'number' ? relay.input.cpu.toFixed(1) : '-'}</td>`;
                            html += `<td rowspan="${relay.outputs.length}" style="padding:6px 8px; background:${inputBg}; vertical-align:middle; text-align:center;">${inputStatus === 'Running' && typeof relay.input.mem === 'number' ? Math.round(relay.input.mem / (1024 * 1024)) : '-'}</td>`;
                            html += `<td rowspan="${relay.outputs.length}" style="padding:6px 8px; background:${inputBg}; vertical-align:middle; text-align:center;">${inputStatus === 'Running' && typeof relay.input.speed === 'number' ? relay.input.speed.toFixed(2) + 'x' : '-'}</td>`;
                            html += `<td rowspan="${relay.outputs.length}" style="padding:6px 8px; background:${inputBg}; vertical-align:middle; text-align:center;">
        <button class="deleteInputBtn" data-input="${input}" data-input-name="${inputName}" title="Delete Input" style="padding:4px 6px; min-width:auto; min-height:auto; background:#dc3545; color:white; border:none; border-radius:3px; font-size:0.8em;"><span class="material-icons" style="font-size:16px;">delete</span></button>
    </td>`;
                        }
                        // Output columns
                        html += `<td class="output-cell" style="word-break:break-all;">
                                <div style="display:flex; flex-direction:column; align-items:center; gap:8px;">
                                    <div title="${out.output_url}" style="font-weight:bold; color:#1976d2;">${out.output_name || out.output_url}</div>
                                </div>
                            </td>
                            <td class="output-cell">${getStatusBadge(outputStatus)}</td>
                            <td class="output-cell">${outputStatus === 'Running' && typeof out.cpu === 'number' ? out.cpu.toFixed(1) : '-'}</td>
                            <td class="output-cell">${outputStatus === 'Running' && typeof out.mem === 'number' ? Math.round(out.mem / (1024 * 1024)) : '-'}</td>
                            <td class="output-cell">${outputStatus === 'Running' && typeof out.bitrate === 'number' ? Math.round(out.bitrate) : '-'}</td>
                            <td class="output-cell">
                                <div style="display:flex; flex-direction:row; align-items:center; justify-content:center; gap:8px; flex-wrap:nowrap;">
                                    ${outputStatus === 'Running'
                                    ? `<button class="stopRelayBtn relay-action-btn" data-input="${input}" data-output="${out.output_url}" data-input-name="${inputName}" data-output-name="${out.output_name || ''}" title="Stop Output"><span class="material-icons" style="font-size:16px;">stop</span></button>`
                                    : `<button class="startRelayBtn relay-action-btn" data-input="${input}" data-output="${out.output_url}" data-input-name="${inputName}" data-output-name="${out.output_name || ''}" title="Start Output"><span class="material-icons" style="font-size:16px;">play_arrow</span></button>`
                                    }
                                    <button class="deleteOutputBtn" data-input="${input}" data-output="${out.output_url}" data-input-name="${inputName}" data-output-name="${out.output_name || ''}" title="Delete Output"><span class="material-icons" style="font-size:16px;">delete</span></button>
                                </div>
                            </td>`;
                    });
                }
            });
            html += '</tbody></table>';
        }
        document.getElementById('relayTable').innerHTML = html;
        attachRelayButtonHandlers();
        addInputHighlightHandlers();
    }

    function addInputHighlightHandlers() {
        // Remove previous handlers to avoid stacking
        document.querySelectorAll('tr[data-input-group], .input-group-row').forEach(el => {
            el.onmouseenter = null;
            el.onmouseleave = null;
        });

        // --- INPUT HOVER: highlight all input columns and all corresponding outputs ---
        document.querySelectorAll('.input-group-row').forEach(inputCell => {
            const group = inputCell.getAttribute('data-input-group');
            inputCell.onmouseenter = function () {
                // Highlight all input columns for this group (the input row)
                const groupRows = document.querySelectorAll(`tr[data-input-group="${group}"]`);
                // Find the input row (rowspan cell)
                const inputRow = document.querySelector(`.input-group-row[data-input-group="${group}"]`);
                if (inputRow) {
                    // Highlight all input columns (first 5 cells)
                    const inputCells = inputRow.parentElement.querySelectorAll('td');
                    for (let i = 0; i < 5 && i < inputCells.length; i++) {
                        inputCells[i].classList.add('input-highlight');
                    }
                }
                // Highlight all output rows for this group (all columns for each output row)
                groupRows.forEach(row => {
                    // For output rows, highlight all output columns (columns 5+)
                    const cells = row.querySelectorAll('td');
                    for (let j = 5; j < cells.length; j++) {
                        cells[j].classList.add('output-highlight');
                    }
                });
            };
            inputCell.onmouseleave = function () {
                const groupRows = document.querySelectorAll(`tr[data-input-group="${group}"]`);
                const inputRow = document.querySelector(`.input-group-row[data-input-group="${group}"]`);
                if (inputRow) {
                    const inputCells = inputRow.parentElement.querySelectorAll('td');
                    for (let i = 0; i < 5 && i < inputCells.length; i++) {
                        inputCells[i].classList.remove('input-highlight');
                    }
                }
                groupRows.forEach(row => {
                    const cells = row.querySelectorAll('td');
                    for (let j = 5; j < cells.length; j++) {
                        cells[j].classList.remove('output-highlight');
                    }
                });
            };
        });

        // --- OUTPUT HOVER: highlight all output columns and all corresponding input columns ---
        document.querySelectorAll('tr[data-input-group]').forEach(row => {
            const group = row.getAttribute('data-input-group');
            const cells = row.querySelectorAll('td');
            // Output columns are 5+
            for (let i = 5; i < cells.length; i++) {
                cells[i].onmouseenter = function () {
                    // Highlight all output columns for this row
                    for (let j = 5; j < cells.length; j++) {
                        cells[j].classList.add('output-highlight');
                    }
                    // Highlight all input columns for the input row
                    const inputRow = document.querySelector(`.input-group-row[data-input-group="${group}"]`);
                    if (inputRow) {
                        const inputCells = inputRow.parentElement.querySelectorAll('td');
                        for (let k = 0; k < 5 && k < inputCells.length; k++) {
                            inputCells[k].classList.add('input-highlight');
                        }
                    }
                };
                cells[i].onmouseleave = function () {
                    for (let j = 5; j < cells.length; j++) {
                        cells[j].classList.remove('output-highlight');
                    }
                    const inputRow = document.querySelector(`.input-group-row[data-input-group="${group}"]`);
                    if (inputRow) {
                        const inputCells = inputRow.parentElement.querySelectorAll('td');
                        for (let k = 0; k < 5 && k < inputCells.length; k++) {
                            inputCells[k].classList.remove('input-highlight');
                        }
                    }
                };
            }
        });
    }

    // Fix: search bar event handler just calls updateUI with latest data
    document.getElementById('searchBox').addEventListener('input', function () {
        if (window.latestRelayStatus) {
            updateUI(window.latestRelayStatus);
        }
    });

    // Initial fetch to populate UI
    fetchStatus();
    // Periodically refresh status every 3 seconds
    setInterval(fetchStatus, 3000);
});
