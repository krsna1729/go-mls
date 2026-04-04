// State management module - centralized application state
const AppState = (() => {
    let currentState = {
        relays: [],
        stats: {},
        search: '',
    };

    return {
        getRelays: () => currentState.relays,
        setRelays: (relays) => { currentState.relays = relays; },

        getStats: () => currentState.stats,
        setStats: (stats) => { currentState.stats = stats; },

        getSearch: () => currentState.search,
        setSearch: (search) => { currentState.search = search; },

        update: (data) => {
            if (data.relays) currentState.relays = data.relays;
            if (data.server) currentState.stats = data.server;
        },

        getState: () => ({ ...currentState }),
    };
})();
