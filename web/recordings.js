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
        if (window.latestRelayStatus && window.latestRelayStatus.relays) {
            renderInputUrls(window.latestRelayStatus.relays, allRecordings);
        } else {
            fetch('/api/relay/status')
                .then(r => r.json())
                .then(data => renderInputUrls(data.relays || [], allRecordings));
        }
    }
    // Listen for relayStatusUpdated event from app.js
    window.addEventListener('relayStatusUpdated', function () {
        const allRecordings = window.allRecordingsList || [];
        if (window.latestRelayStatus && window.latestRelayStatus.relays) {
            renderInputUrls(window.latestRelayStatus.relays, allRecordings);
        }
    });

    // Listen for all recordings update
    window.updateAllRecordingsList = function(list) {
        window.allRecordingsList = list;
        // Also update input table if relays are present
        if (window.latestRelayStatus && window.latestRelayStatus.relays) {
            renderInputUrls(window.latestRelayStatus.relays, list);
        }
    };

    function renderInputUrls(relays, allRecordings) {
        // Sort by input_name (fallback input_url), case-insensitive, natural order
        relays.sort((a, b) => {
            const aName = a.input_name || a.input_url || '';
            const bName = b.input_name || b.input_url || '';
            return aName.localeCompare(bName, undefined, { numeric: true, sensitivity: 'base' });
        });
        const search = document.getElementById('inputSearchBox').value.trim().toLowerCase();
        let html = '<table style="width:100%"><thead><tr><th>Name</th><th>URL</th><th>Action</th></tr></thead><tbody>';
        for (const relay of relays) {
            if (search && !relay.input_name.toLowerCase().includes(search) && !relay.input_url.toLowerCase().includes(search)) continue;
            // Find all recordings for this input, sorted by started_at descending
            let latestActive = null;
            let latestCompleted = null;
            if (Array.isArray(allRecordings)) {
                const matches = allRecordings.filter(r => r.name === relay.input_name && r.source === relay.input_url)
                    .sort((a, b) => new Date(b.started_at) - new Date(a.started_at));
                latestActive = matches.find(r => r.active);
                latestCompleted = matches.find(r => !r.active);
            }
            // Toggle button logic
            let toggleBtn = '';
            if (latestActive) {
                toggleBtn = `<button class=\"toggleRecBtn active\" data-name=\"${relay.input_name}\" data-url=\"${relay.input_url}\"><span class=\"rec-dot\"></span>Stop</button>`;
            } else {
                toggleBtn = `<button class=\"toggleRecBtn\" data-name=\"${relay.input_name}\" data-url=\"${relay.input_url}\"><span class=\"material-icons\">fiber_manual_record</span>Start</button>`;
            }
            // Download button logic
            let downloadBtn = '';
            if (latestCompleted) {
                downloadBtn = `<button class=\"downloadLatestBtn\" data-filename=\"${encodeURIComponent(latestCompleted.filename)}\"><span class=\"material-icons\">download</span>Download</button>`;
            } else {
                downloadBtn = `<button class=\"downloadLatestBtn\" disabled style=\"opacity:0.5;cursor:not-allowed;\"><span class=\"material-icons\">download</span>Download</button>`;
            }
            html += `<tr>
                <td>${relay.input_name}</td>
                <td>${relay.input_url}</td>
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
                if (btn.classList.contains('active')) {
                    // Stop
                    fetch('/api/recording/stop', {
                        method: 'POST',
                        headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify({ name, source: url })
                    }).then(fetchInputUrls);
                } else {
                    // Start
                    fetch('/api/recording/start', {
                        method: 'POST',
                        headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify({ name, source: url })
                    }).then(fetchInputUrls);
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
        let html = '<table style="width:100%"><thead><tr><th>Name</th><th>Source</th><th>Started</th><th>Size</th><th>Status</th><th>Action</th></tr></thead><tbody>';
        for (const rec of list) {
            if (search && !rec.name.toLowerCase().includes(search) && !rec.source.toLowerCase().includes(search) && !new Date(rec.started_at).toLocaleString().toLowerCase().includes(search)) continue;
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
            html += `<tr>
                <td>${rec.name}</td>
                <td>${rec.source}</td>
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
