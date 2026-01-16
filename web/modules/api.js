// API abstraction layer - all HTTP requests go through this module
const API = (() => {
    // API version prefix - change here to update all endpoints
    const API_BASE = '/api/v1';

    const call = async (endpoint, method = 'GET', data = null) => {
        const opts = { method, headers: { 'Content-Type': 'application/json' } };
        if (data) opts.body = JSON.stringify(data);
        const res = await fetch(API_BASE + endpoint, opts);
        return res.json();
    };

    const callForm = async (endpoint, formData) => {
        const res = await fetch(API_BASE + endpoint, { method: 'POST', body: formData });
        return res;
    };

    return {
        // Relay APIs
        startRelay: (config) => call('/relay/start', 'POST', config),
        stopRelay: (ids) => call('/relay/stop', 'POST', ids),
        getStatus: () => call('/relay/status'),
        exportConfig: () => API_BASE + '/relay/export',
        importConfig: (file) => {
            const formData = new FormData();
            formData.append('file', file);
            return callForm('/relay/import', formData);
        },
        getPresets: () => call('/relay/presets'),
        deleteInput: (input) => call('/relay/delete-input', 'POST', input),
        deleteOutput: (output) => call('/relay/delete-output', 'POST', output),

        // HLS APIs
        startHLSViewer: (inputName) => call('/relay/hls/start-viewer', 'POST', { input_name: inputName }),
        stopHLSViewer: (inputName, viewerId) => call('/relay/hls/stop-viewer', 'POST', { input_name: inputName, viewer_id: viewerId }),
        heartbeat: (inputName, viewerId) => call('/relay/hls/heartbeat', 'POST', { input_name: inputName, viewer_id: viewerId }),

        // Recording APIs
        getRecordings: () => call('/recording/list'),
        startRecording: (name, source) => call('/recording/start', 'POST', { name, source }),
        stopRecording: (name, source) => call('/recording/stop', 'POST', { name, source }),
        deleteRecording: (filename) => call('/recording/delete', 'POST', { filename }),

        // Utility
        getApiBase: () => API_BASE,
    };
})();

