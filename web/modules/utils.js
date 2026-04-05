// Utility functions module - formatting and helpers
const Utils = (() => {
    const formatBytes = (bytes) => {
        if (bytes < 1024) return bytes + ' B';
        if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
        if (bytes < 1024 * 1024 * 1024) return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
        return (bytes / (1024 * 1024 * 1024)).toFixed(1) + ' GB';
    };

    const formatBitrate = (kbps) => {
        if (kbps >= 1000) return (kbps / 1000).toFixed(2) + ' Mbps';
        if (kbps > 0) return Math.round(kbps) + ' kbps';
        return '0 kbps';
    };

    const getStatusBadge = (status) => {
        if (status === 'Running') return '<span class="badge badge-running">Running</span>';
        if (status === 'Starting') return '<span class="badge badge-unknown">Starting</span>';
        if (status === 'Active') return '<span class="badge badge-running">Running</span>';
        if (status === 'Stopped') return '<span class="badge badge-stopped">Stopped</span>';
        if (status === 'Error') return '<span class="badge badge-error">Error</span>';
        return '<span class="badge badge-unknown">Unknown</span>';
    };

    const filterData = (data, query) => {
        if (!query) return data;
        const q = query.toLowerCase();
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
    };

    return {
        formatBytes,
        formatBitrate,
        getStatusBadge,
        filterData,
    };
})();
