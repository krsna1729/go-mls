// recordings.js - Handles the UI and API for the Recordings tab

document.addEventListener('DOMContentLoaded', function () {
    // Only run if the recordings tab is present
    const recordingsTab = document.getElementById('recordingsTab');
    if (!recordingsTab) return;

    // Wrap all content in the recordings tab in a white card
    const card = document.createElement('div');
    card.className = 'recordings-card';
    while (recordingsTab.firstChild) {
        card.appendChild(recordingsTab.firstChild);
    }
    // --- Inputs Section ---
    const inputSection = document.createElement('div');
    inputSection.innerHTML = `
        <h2>Inputs</h2>
        <input type="text" id="inputSearchBox" placeholder="Search Inputs by name or URL" style="width:60%;margin-bottom:1em;">
        <div id="inputUrlList"></div>
    `;
    card.appendChild(inputSection); // <-- append to card, not recordingsTab

    // --- All Recordings Section ---
    const allRecordingsSection = document.createElement('div');
    allRecordingsSection.innerHTML = `
        <h2>All Recordings</h2>
        <input type="text" id="recordingSearchBox" placeholder="Search recordings by name, source, or date" style="width:60%;margin-bottom:1em;">
        <div id="allRecordingsList"></div>
    `;
    card.appendChild(allRecordingsSection); // <-- append to card, not recordingsTab

    recordingsTab.appendChild(card); // append the card with both sections

    // --- Fetch and Render Input URLs ---
    function fetchInputUrls() {
        // Try to get all recordings from window if available
        const allRecordings = window.allRecordingsList || [];
        // Support new relay status API ({server, relays: [{input, outputs}]})
        if (window.latestRelayStatus && window.latestRelayStatus.relays) {
            renderInputUrlsV2(window.latestRelayStatus.relays, allRecordings);
        } else {
            fetch('/api/relay/status')
                .then(r => r.json())
                .then(data => {
                    if (data && data.relays) {
                        renderInputUrlsV2(data.relays, allRecordings);
                    } else {
                        console.warn('No relay data found in status response');
                        renderInputUrlsV2([], allRecordings);
                    }
                })
                .catch(err => {
                    console.error('Failed to fetch relay status:', err);
                    renderInputUrlsV2([], allRecordings);
                });
        }
    }
    // Listen for relayStatusUpdated event from app.js
    window.addEventListener('relayStatusUpdated', function () {
        const allRecordings = window.allRecordingsList || [];
        if (window.latestRelayStatus && window.latestRelayStatus.relays) {
            renderInputUrlsV2(window.latestRelayStatus.relays, allRecordings);
        }
    });

    // Listen for all recordings update
    window.updateAllRecordingsList = function(list) {
        window.allRecordingsList = list;
        // Also update input table if relays are present
        if (window.latestRelayStatus && window.latestRelayStatus.relays) {
            renderInputUrlsV2(window.latestRelayStatus.relays, list);
        }
    };

    function renderInputUrlsV2(relays, allRecordings) {
        // relays: [{input, outputs}]
        if (!Array.isArray(relays)) {
            console.warn('renderInputUrlsV2: relays is not an array', relays);
            relays = [];
        }
        
        relays.sort((a, b) => {
            const aName = (a.input && a.input.input_name) || (a.input && a.input.input_url) || '';
            const bName = (b.input && b.input.input_name) || (b.input && b.input.input_url) || '';
            return aName.localeCompare(bName, undefined, { numeric: true, sensitivity: 'base' });
        });
        
        const search = document.getElementById('inputSearchBox').value.trim().toLowerCase();
        let html = '<table style="width:100%"><thead><tr><th>Name</th><th>URL</th><th>Status</th><th>Action</th></tr></thead><tbody>';
        
        for (const relay of relays) {
            const input = relay.input;
            if (!input || !input.input_name || !input.input_url) {
                console.warn('Skipping relay with invalid input data:', relay);
                continue;
            }
            
            if (search && !input.input_name.toLowerCase().includes(search) && !input.input_url.toLowerCase().includes(search)) continue;
            
            // Find all recordings for this input, sorted by started_at descending
            let latestActive = null;
            let latestCompleted = null;
            if (Array.isArray(allRecordings)) {
                const matches = allRecordings.filter(r => r.name === input.input_name && r.source === input.input_url)
                    .sort((a, b) => new Date(b.started_at) - new Date(a.started_at));
                latestActive = matches.find(r => r.active);
                latestCompleted = matches.find(r => !r.active);
            }
            
            // Toggle button logic
            let toggleBtn = '';
            if (latestActive) {
                toggleBtn = `<button class=\"toggleRecBtn active\" data-name=\"${input.input_name}\" data-url=\"${input.input_url}\"><span class=\"rec-dot\"></span>Stop</button>`;
            } else {
                toggleBtn = `<button class=\"toggleRecBtn\" data-name=\"${input.input_name}\" data-url=\"${input.input_url}\"><span class=\"material-icons\">fiber_manual_record</span>Start</button>`;
            }
            
            // Download button logic
            let downloadBtn = '';
            if (latestCompleted) {
                downloadBtn = `<button class=\"downloadLatestBtn\" data-filename=\"${encodeURIComponent(latestCompleted.filename)}\"><span class=\"material-icons\">download</span>Download</button>`;
            } else {
                downloadBtn = `<button class=\"downloadLatestBtn\" disabled style=\"opacity:0.5;cursor:not-allowed;\"><span class=\"material-icons\">download</span>Download</button>`;
            }
            
            html += `<tr>
                <td>${input.input_name}</td>
                <td>${input.input_url}</td>
                <td>${input.status || 'Unknown'}${input.last_error ? `<br><span style='color:red'>${input.last_error}</span>` : ''}</td>
                <td>
                    ${toggleBtn}
                    ${downloadBtn}
                </td>
            </tr>`;
        }
        html += '</tbody></table>';
        document.getElementById('inputUrlList').innerHTML = html;
        attachInputUrlHandlers();
    }

    function attachInputUrlHandlers() {
        document.querySelectorAll('.toggleRecBtn').forEach(btn => {
            btn.onclick = function () {
                const name = btn.getAttribute('data-name');
                const url = btn.getAttribute('data-url');
                
                // Add validation to prevent undefined values
                if (!name || !url || name === 'undefined' || url === 'undefined') {
                    console.error('Invalid recording data: name=' + name + ', url=' + url);
                    alert('Cannot start recording: Invalid input data');
                    return;
                }
                
                if (btn.classList.contains('active')) {
                    // Stop
                    fetch('/api/recording/stop', {
                        method: 'POST',
                        headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify({ name, source: url })
                    }).then(() => {
                        // Wait a bit for the recording to actually stop, then refresh
                        setTimeout(() => {
                            fetchInputUrls();
                            fetchAllRecordings();
                        }, 500);
                    });
                } else {
                    // Start
                    fetch('/api/recording/start', {
                        method: 'POST',
                        headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify({ name, source: url })
                    }).then(() => {
                        // Wait a bit for the recording to actually start, then refresh
                        setTimeout(() => {
                            fetchInputUrls();
                            fetchAllRecordings();
                        }, 500);
                    });
                }
            };
        });
        document.querySelectorAll('.downloadLatestBtn').forEach(btn => {
            if (btn.disabled) return;
            btn.onclick = function () {
                const filename = btn.getAttribute('data-filename');
                if (filename) {
                    window.location = '/api/recording/download?filename=' + filename;
                }
            };
        });
    }
    document.getElementById('inputSearchBox').addEventListener('input', fetchInputUrls);
    // Fetch input URLs immediately on load
    fetchInputUrls(); // <-- Ensure input table loads on fresh open

    // --- Fetch and Render All Recordings ---
    function fetchAllRecordings() {
        fetch('/api/recording/list')
            .then(r => r.json())
            .then(renderAllRecordings);
    }

    function renderAllRecordings(list) {
        // Keep all recordings globally for input table
        if (typeof window.updateAllRecordingsList === 'function') {
            window.updateAllRecordingsList(list);
        }
        // Sort by started_at (latest first)
        list.sort((a, b) => new Date(b.started_at) - new Date(a.started_at));
        const search = document.getElementById('recordingSearchBox').value.trim().toLowerCase();
        let html = '<table style="width:100%"><thead><tr><th>Filename</th><th>Started</th><th>Size</th><th>Status</th><th>Action</th></tr></thead><tbody>';
        for (const rec of list) {
            if (search && !rec.filename.toLowerCase().includes(search) && !rec.name.toLowerCase().includes(search) && !new Date(rec.started_at).toLocaleString().toLowerCase().includes(search)) continue;
            let sizeStr = rec.file_size ? (rec.file_size / (1024 * 1024)).toFixed(2) + ' MB' : '';
            let downloadBtn = '';
            let deleteBtn = '';
            // Use filename for deletion (no key construction)
            const filename = rec.filename;
            if (rec.active) {
                downloadBtn = `<button class=\"downloadRecordingBtn\" disabled style=\"opacity:0.5;cursor:not-allowed;\"><span class=\"material-icons\">download</span></button>`;
                deleteBtn = `<button class=\"deleteRecordingBtn\" disabled style=\"opacity:0.5;cursor:not-allowed;\"><span class=\"material-icons\">delete</span></button>`;
            } else {
                downloadBtn = `<button class=\"downloadRecordingBtn\" data-filename=\"${encodeURIComponent(rec.filename)}\"><span class=\"material-icons\">download</span></button>`;
                deleteBtn = `<button class=\"deleteRecordingBtn\" data-filename=\"${encodeURIComponent(rec.filename)}\"><span class=\"material-icons\">delete</span></button>`;
            }
            // Show source on hover if available
            const titleAttr = rec.source ? `title="Source: ${rec.source}"` : '';
            html += `<tr>
                <td ${titleAttr}>${rec.filename || rec.name}</td>
                <td>${new Date(rec.started_at).toLocaleString()}</td>
                <td>${sizeStr}</td>
                <td>${rec.active ? '<span style=\"color:red;\">Active</span>' : 'Stopped'}</td>
                <td>
                    ${downloadBtn}
                    ${deleteBtn}
                </td>
            </tr>`;
        }
        html += '</tbody></table>';
        document.getElementById('allRecordingsList').innerHTML = html;
        document.querySelectorAll('.downloadRecordingBtn').forEach(btn => {
            if (btn.disabled) return;
            btn.onclick = function () {
                const filename = btn.getAttribute('data-filename');
                window.location = '/api/recording/download?filename=' + filename;
            };
        });
        document.querySelectorAll('.deleteRecordingBtn').forEach(btn => {
            if (btn.disabled) return;
            btn.onclick = function () {
                const filename = decodeURIComponent(btn.getAttribute('data-filename'));
                if (confirm('Are you sure you want to delete this recording?')) {
                    fetch('/api/recording/delete', {
                        method: 'POST',
                        headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify({ filename })
                    }).then(() => fetchAllRecordings());
                }
            };
        });
    }
    document.getElementById('recordingSearchBox').addEventListener('input', fetchAllRecordings);
    fetchAllRecordings();

    // Refresh input list when Recordings tab is activated
    const tabRecordings = document.getElementById('tabRecordings');
    if (tabRecordings) {
        tabRecordings.addEventListener('click', function () {
            fetchInputUrls(); // Always refresh inputs when tab is clicked
        });
    }

    // --- Setup Server-Sent Events (SSE) ---
    function setupRecordingsSSE() {
        if (!!window.EventSource) {
            const es = new EventSource('/api/recording/sse');
            es.onmessage = function (event) {
                if (event.data === 'update') {
                    fetchAllRecordings();
                }
            };
        }
    }
    setupRecordingsSSE();
});
