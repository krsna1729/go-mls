// API abstraction layer - all HTTP requests go through this module
const API = (() => {
    const call = async (endpoint, method = 'GET', data = null) => {
        const opts = { method, headers: { 'Content-Type': 'application/json' } };
        if (data) opts.body = JSON.stringify(data);
        const res = await fetch(endpoint, opts);
        if (!res.ok) {
            const err = await res.json().catch(() => ({ error: res.statusText }));
            throw new Error(err.error || 'Request failed');
        }
        return res.json();
    };

    const callText = async (endpoint, method = 'GET') => {
        const res = await fetch(endpoint, { method });
        if (!res.ok) {
            throw new Error('Request failed');
        }
        return res.text();
    };

    return {
        // === Inputs ===

        createInput: (streamPath, remoteUrl = null) => {
            const body = { stream_path: streamPath };
            if (remoteUrl) body.remote_url = remoteUrl;
            return call('/inputs', 'POST', body);
        },

        listInputs: () => call('/inputs'),

        deleteInput: (streamPath) => call(`/inputs?stream=${encodeURIComponent(streamPath)}`, 'DELETE'),

        // === Outputs ===

        createOutput: (streamPath, outputId, remoteUrl) => call('/outputs', 'POST', {
            stream_path: streamPath,
            output_id: outputId,
            remote_url: remoteUrl
        }),

        listOutputs: (streamPath = null) => {
            const url = streamPath ? `/outputs?stream=${encodeURIComponent(streamPath)}` : '/outputs';
            return call(url);
        },

        deleteOutput: (streamPath, outputId) => 
            call(`/outputs?stream=${encodeURIComponent(streamPath)}&id=${encodeURIComponent(outputId)}`, 'DELETE'),

        // === Recording ===

        startRecording: (streamPath) => call(`/record?stream=${encodeURIComponent(streamPath)}`, 'POST'),

        stopRecording: (streamPath) => call(`/record?stream=${encodeURIComponent(streamPath)}`, 'DELETE'),

        // === HLS ===

        startHLS: (streamPath) => call(`/hls/start?stream=${encodeURIComponent(streamPath)}`),

        stopHLS: (streamPath, viewerId = 'auto') => call('/hls/stop', 'POST', {
            stream: streamPath,
            viewer_id: viewerId
        }),

        // === Stats ===

        getStats: () => call('/stats'),

        // === Config Import/Export ===

        exportConfig: () => call('/system/export'),

        importConfig: (configData) => call('/system/import', 'POST', configData),

        // === Health ===

        healthCheck: () => callText('/stats').then(() => true).catch(() => false),
    };
})();
