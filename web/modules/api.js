// API abstraction layer - maps frontend calls to new REST API
const API = (() => {
    const BASE = '';

    const fetchJSON = async (url, method = 'GET', body = null) => {
        const opts = { method, headers: { 'Content-Type': 'application/json' } };
        if (body) opts.body = JSON.stringify(body);
        const resp = await fetch(BASE + url, opts);
        const text = await resp.text();
        let data = null;

        if (text) {
            try {
                data = JSON.parse(text);
            } catch {
                if (!resp.ok) {
                    const err = new Error(text);
                    err.status = resp.status;
                    throw err;
                }
                data = text;
            }
        }

        if (!resp.ok) {
            const message = data && (data.error || data.message) ? (data.error || data.message) : `Request failed (${resp.status})`;
            const err = new Error(message);
            err.status = resp.status;
            err.body = data;
            throw err;
        }

        return data;
    };

    const transformStats = (data) => {
        const relays = [];
        const inputsByPath = {};

        for (const input of (data.inputs || [])) {
            const inputStatus = input.status === 'Active' ? 'Running' : (input.status || 'Stopped');
            inputsByPath[input.stream_path] = {
                input: {
                    input_url: input.remote_url || input.remote_addr || '',
                    input_name: input.stream_path,
                    status: inputStatus,
                    cpu: input.telemetry?.cpu || 0,
                    mem: (input.telemetry?.mem_mb || 0) * 1024 * 1024,
                    speed: input.telemetry?.speed || 0,
                    bitrate: (input.telemetry?.bitrate || 0) / 1000,
                    last_error: input.last_error || ''
                },
                outputs: []
            };
        }

        for (const output of (data.outputs || [])) {
            const relay = inputsByPath[output.stream_path];
            if (relay) {
                relay.outputs.push({
                    output_url: output.remote_url || '',
                    output_name: output.output_id,
                    status: output.status || 'Stopped',
                    cpu: output.telemetry?.cpu || 0,
                    mem: (output.telemetry?.mem_mb || 0) * 1024 * 1024,
                    bitrate: (output.telemetry?.bitrate || 0) / 1000,
                    last_error: output.last_error || ''
                });
            }
        }

        for (const key in inputsByPath) {
            relays.push(inputsByPath[key]);
        }

        return {
            server: {
                cpu: data.server?.cpu || 0,
                mem: (data.server?.mem_mb || 0) * 1024 * 1024
            },
            relays,
            presets: {}
        };
    };

    return {
        startInput: async (config) => {
            if (!config.input_name) {
                throw new Error('input_name is required');
            }
            return fetchJSON('/inputs', 'POST', {
                stream_path: config.input_name,
                remote_url: config.input_url || ''
            });
        },

        startOutput: async (config) => {
            if (!config.input_name) {
                throw new Error('input_name is required');
            }
            if (!config.output_url) {
                throw new Error('output_url is required');
            }
            const outputId = config.output_name || 'default';
            const outputConfig = {
                stream_path: config.input_name,
                output_id: outputId,
                remote_url: config.output_url
            };
            if (config.preset) outputConfig.preset = config.preset;
            if (config.video_codec) outputConfig.video_codec = config.video_codec;
            if (config.audio_codec) outputConfig.audio_codec = config.audio_codec;
            if (config.resolution) outputConfig.resolution = config.resolution;
            if (config.framerate) outputConfig.framerate = config.framerate;
            if (config.bitrate) outputConfig.bitrate = config.bitrate;
            if (config.rotation) outputConfig.rotation = config.rotation;
            return fetchJSON('/outputs', 'POST', outputConfig);
        },

        startRelay: async (config) => {
            return fetchJSON('/outputs/start', 'POST', {
                stream_path: config.input_name,
                output_id: config.output_name || 'default'
            });
        },

        stopRelay: async (ids) => {
            return fetchJSON('/outputs/stop', 'POST', {
                stream_path: ids.input_name,
                output_id: ids.output_name || 'default'
            });
        },

        getStatus: async () => {
            const data = await fetchJSON('/stats');
            return transformStats(data);
        },

        exportConfig: () => BASE + '/system/export',

        importConfig: async (file) => {
            const text = await file.text();
            const config = JSON.parse(text);
            return fetchJSON('/system/import', 'POST', config);
        },

        getPresets: () => fetchJSON('/presets'),

        deleteInput: async (input) => {
            const name = input.input_name || input.input_url;
            return fetchJSON(`/inputs?stream=${encodeURIComponent(name)}`, 'DELETE');
        },

        deleteOutput: async (output) => {
            const stream = output.input_name || output.input_url;
            const id = output.output_name || 'default';
            return fetchJSON(`/outputs?stream=${encodeURIComponent(stream)}&id=${encodeURIComponent(id)}`, 'DELETE');
        },

        startHLSViewer: (inputName) => {
            return fetchJSON(`/hls/start?stream=${encodeURIComponent(inputName)}`, 'POST');
        },

        stopHLSViewer: (inputName, viewerId) => {
            return fetchJSON('/hls/stop', 'POST', { stream: inputName, viewer_id: viewerId });
        },

        heartbeat: async (inputName, viewerId) => {
            const resp = await fetch(BASE + '/hls/heartbeat', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ stream: inputName, viewer_id: viewerId })
            });
            let data = null;
            const text = await resp.text();
            if (text) {
                try {
                    data = JSON.parse(text);
                } catch {
                    data = { error: text };
                }
            }
            return {
                ok: resp.ok,
                status: resp.status,
                body: data
            };
        },

        getRecordings: () => fetchJSON('/recordings'),

        startRecording: (name, source) => {
            return fetchJSON(`/record?stream=${encodeURIComponent(name)}`, 'POST');
        },

        stopRecording: (name, source) => {
            return fetchJSON(`/record?stream=${encodeURIComponent(name)}`, 'DELETE');
        },

        deleteRecording: (filename) => fetchJSON(`/recordings?filename=${encodeURIComponent(filename)}`, 'DELETE'),
    };
})();
