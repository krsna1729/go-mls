// API abstraction layer - all HTTP requests go through this module
const API = (() => {
    const call = async (endpoint, method = 'GET', data = null) => {
        const opts = { method, headers: { 'Content-Type': 'application/json' } };
        if (data) opts.body = JSON.stringify(data);
        const res = await fetch(endpoint, opts);
        return res.json();
    };

    const callForm = async (endpoint, formData) => {
        const res = await fetch(endpoint, { method: 'POST', body: formData });
        return res;
    };

    return {
        // Relay APIs
        startRelay: (config) => call('/api/relay/start', 'POST', config),
        stopRelay: (ids) => call('/api/relay/stop', 'POST', ids),
        getStatus: () => call('/api/relay/status'),
        exportConfig: () => '/api/relay/export',
        importConfig: (file) => {
            const formData = new FormData();
            formData.append('file', file);
            return callForm('/api/relay/import', formData);
        },
        getPresets: () => call('/api/relay/presets'),
        deleteInput: (input) => call('/api/relay/delete-input', 'POST', input),
        deleteOutput: (output) => call('/api/relay/delete-output', 'POST', output),

        // HLS APIs
        startHLSViewer: (inputName) => call('/api/relay/hls/start-viewer', 'POST', { input_name: inputName }),
        stopHLSViewer: (viewerId) => call('/api/relay/hls/stop-viewer', 'POST', { viewer_id: viewerId }),
        heartbeat: (viewerId) => call('/api/relay/hls/heartbeat', 'POST', { viewer_id: viewerId }),
    };
})();
