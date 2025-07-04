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
            const buttonKey = `${input.input_name}_${input.input_url}`;
            
            // Check if this button is in "starting" state
            if (startingButtons.has(buttonKey)) {
                toggleBtn = `<button class="toggleRecBtn starting" data-name="${input.input_name}" data-url="${input.input_url}" disabled><span class="material-icons">hourglass_empty</span>Starting...</button>`;
            } else if (latestActive) {
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

    // Add debouncing to prevent rapid successive requests
    const recordingRequestTimestamps = new Map();
    const REQUEST_DEBOUNCE_MS = 1000; // 1 second debounce
    
    // Track buttons that are in "starting" state to preserve them during re-renders
    const startingButtons = new Map(); // key: "name_url", value: {timeout, originalText}

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
                
                // Check for rapid successive requests (debouncing)
                const requestKey = `${name}_${url}`;
                const now = Date.now();
                const lastRequest = recordingRequestTimestamps.get(requestKey);
                if (lastRequest && (now - lastRequest) < REQUEST_DEBOUNCE_MS) {
                    console.log('Ignoring rapid successive request for:', requestKey);
                    return;
                }
                recordingRequestTimestamps.set(requestKey, now);
                
                // Prevent double-clicks by disabling the button temporarily
                if (btn.disabled) {
                    return;
                }
                btn.disabled = true;
                
                // Store original button text to restore later
                const originalText = btn.innerHTML;
                
                if (btn.classList.contains('active')) {
                    // Stop recording
                    btn.innerHTML = '<span class="material-icons">hourglass_empty</span>Stopping...';
                    fetch('/api/recording/stop', {
                        method: 'POST',
                        headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify({ name, source: url })
                    }).then(response => {
                        if (!response.ok) {
                            return response.text().then(text => {
                                throw new Error(`HTTP ${response.status}: ${text || response.statusText}`);
                            });
                        }
                        return response.json();
                    }).then(() => {
                        // Wait a bit for the recording to actually stop, then refresh
                        setTimeout(() => {
                            fetchInputUrls();
                            fetchAllRecordings();
                        }, 500);
                    }).catch((error) => {
                        console.error('Error stopping recording:', error);
                        // Always refresh the UI even if there was an error
                        // This helps when the recording has already finished naturally
                        setTimeout(() => {
                            fetchInputUrls();
                            fetchAllRecordings();
                        }, 200);
                        
                        if (error.message.includes('already exists')) {
                            // Recording is already running - just refresh UI silently
                            console.log('Recording is already running, refreshing UI');
                            setTimeout(() => {
                                fetchInputUrls();
                                fetchAllRecordings();
                            }, 100);
                        } else if (error.message.includes('no active recording') || error.message.includes('already finished') || error.message.includes('finished naturally')) {
                            // Don't show an error for recordings that have already finished
                            console.log('Recording has already finished');
                        } else {
                            alert('Failed to stop recording: ' + error.message);
                        }
                        btn.innerHTML = originalText;
                    }).finally(() => {
                        btn.disabled = false;
                    });
                } else {
                    // Start recording
                    const buttonKey = `${name}_${url}`;
                    
                    // Set button to starting state
                    btn.innerHTML = '<span class="material-icons">hourglass_empty</span>Starting...';
                    btn.disabled = true;
                    
                    // Start polling for recording status immediately
                    const startTime = Date.now();
                    const maxWaitTime = 20000; // 20 seconds max
                    let pollInterval;
                    
                    const pollForRecording = () => {
                        fetch('/api/recording/list')
                            .then(r => r.json())
                            .then(recordings => {
                                // Check if a recording with this name and source exists and is active
                                const recordingExists = recordings.some(rec => 
                                    rec.name === name && rec.source === url && rec.active
                                );
                                
                                if (recordingExists) {
                                    // Recording has started successfully - clear polling and update UI
                                    clearInterval(pollInterval);
                                    clearTimeout(timeoutId);
                                    startingButtons.delete(buttonKey);
                                    fetchInputUrls();
                                    fetchAllRecordings();
                                    return;
                                }
                                
                                // Check if we've exceeded the maximum wait time
                                const elapsed = Date.now() - startTime;
                                if (elapsed > maxWaitTime) {
                                    // Timeout - recording may have failed to start
                                    clearInterval(pollInterval);
                                    clearTimeout(timeoutId);
                                    startingButtons.delete(buttonKey);
                                    fetchInputUrls();
                                    fetchAllRecordings();
                                    return;
                                }
                            })
                            .catch(err => {
                                console.error('Error polling for recording status:', err);
                                // Continue polling even on error unless timeout reached
                                const elapsed = Date.now() - startTime;
                                if (elapsed > maxWaitTime) {
                                    clearInterval(pollInterval);
                                    clearTimeout(timeoutId);
                                    startingButtons.delete(buttonKey);
                                    fetchInputUrls();
                                    fetchAllRecordings();
                                }
                            });
                    };
                    
                    // Fallback timeout in case polling fails
                    const timeoutId = setTimeout(() => {
                        clearInterval(pollInterval);
                        startingButtons.delete(buttonKey);
                        fetchInputUrls();
                        fetchAllRecordings();
                    }, maxWaitTime);
                    
                    startingButtons.set(buttonKey, {
                        timeout: timeoutId,
                        originalText: originalText
                    });
                    
                    fetch('/api/recording/start', {
                        method: 'POST',
                        headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify({ name, source: url })
                    }).then(response => {
                        if (!response.ok) {
                            return response.text().then(text => {
                                throw new Error(`HTTP ${response.status}: ${text || response.statusText}`);
                            });
                        }
                        return response.json();
                    }).then(() => {
                        // Start polling every 1 second after successful API call
                        pollInterval = setInterval(pollForRecording, 1000);
                        // Also poll immediately
                        pollForRecording();
                    }).catch((error) => {
                        console.error('Error starting recording:', error);
                        
                        // Clear timeouts and remove from tracking
                        clearInterval(pollInterval);
                        clearTimeout(timeoutId);
                        startingButtons.delete(buttonKey);
                        
                        if (error.message.includes('already exists')) {
                            // Recording is already running - just refresh UI silently  
                            console.log('Recording is already running, refreshing UI');
                            setTimeout(() => {
                                fetchInputUrls();
                                fetchAllRecordings();
                            }, 100);
                        } else {
                            alert('Failed to start recording: ' + error.message);
                            // Reset button immediately on error
                            btn.innerHTML = originalText;
                            btn.disabled = false;
                        }
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
