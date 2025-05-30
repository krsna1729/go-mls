document.addEventListener('DOMContentLoaded', function() {
    const relayControls = document.getElementById('controls');

    // Render static input controls once
    relayControls.innerHTML = `<h2>Add Relay Endpoint</h2>
        <input type="text" id="inputUrl" placeholder="Input URL" style="width:260px;">
        <input type="text" id="outputUrl" placeholder="Output URL" style="width:260px;">
        <button id="startRelayBtn">Start Relay Endpoint</button>
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
        // data is an array of relays, each with input_url and endpoints
        let html = '';
        if (!data || data.length === 0) {
            html += '<i>No relays running.</i>';
        } else {
            // Sort relays by input_url (alphanumeric ascending)
            data.sort((a, b) => a.input_url.localeCompare(b.input_url, undefined, {numeric: true, sensitivity: 'base'}));
            html += `<table style="width:100%;border-collapse:separate;border-spacing:0;">
                <thead>
                    <tr>
                        <th style="text-align:left; padding:8px 12px;">Input URL</th>
                        <th style="text-align:left; padding:8px 12px;">Output URL</th>
                        <th style="text-align:left; padding:8px 12px;">Status</th>
                        <th style="text-align:left; padding:8px 12px;">Bitrate (kbps)</th>
                        <th style="text-align:left; padding:8px 12px;">Action</th>
                    </tr>
                </thead>
                <tbody>`;
            for (let relayIdx = 0; relayIdx < data.length; relayIdx++) {
                const relay = data[relayIdx];
                const input = relay.input_url;
                // Sort endpoints by output_url (alphanumeric ascending)
                const endpoints = relay.endpoints ? relay.endpoints.slice().sort((a, b) => (a.output_url || '').localeCompare(b.output_url || '', undefined, {numeric: true, sensitivity: 'base'})) : [];
                // Alternate input group background
                const inputBg = relayIdx % 2 === 0 ? '#f7fafd' : '#f0f4fa';
                const inputGroupBorder = 'border-top: 3px solid #b6d0f7;';
                if (endpoints.length === 0) {
                    html += `<tr style="${inputGroupBorder}">
                        <td style="word-break:break-all; color:#1976d2; font-weight:bold; padding:8px 12px; background:${inputBg};">${input}</td>
                        <td colspan="4" style="padding:8px 12px; background:#fff;"><i>No endpoints</i></td>
                    </tr>`;
                } else {
                    for (let i = 0; i < endpoints.length; i++) {
                        const ep = endpoints[i];
                        const outputBg = inputBg;
                        html += `<tr style="${inputGroupBorder} background:${outputBg};">
                            ${i === 0 ? `<td rowspan="${endpoints.length}" style="word-break:break-all; color:#1976d2; font-weight:bold; vertical-align:middle; padding:8px 12px; background:${inputBg}; border:none;">${input}</td>` : ''}
                            <td style="word-break:break-all; padding:8px 12px;">${ep.output_url}</td>
                            <td style="padding:8px 12px;">${ep.running ? 'Running' : 'Stopped'}</td>
                            <td style="padding:8px 12px;">${ep.bitrate ? ep.bitrate : '-'}</td>
                            <td style="padding:8px 12px;">
                                ${ep.running
                                    ? `<button class="stopRelayBtn" data-input="${input}" data-output="${ep.output_url}">Stop</button>`
                                    : `<button class="startRelayBtn" data-input="${input}" data-output="${ep.output_url}">Start</button>`}
                            </td>
                        </tr>`;
                    }
                }
            }
            html += '</tbody></table>';
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
                const input = btn.getAttribute('data-input');
                const output = btn.getAttribute('data-output');
                fetch('/api/relay/stop', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ input_url: input, output_url: output })
                }).then(() => { fetchStatus(); });
            };
        });
        document.querySelectorAll('.startRelayBtn').forEach(btn => {
            btn.onclick = function() {
                const input = btn.getAttribute('data-input');
                const output = btn.getAttribute('data-output');
                fetch('/api/relay/start', {
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
