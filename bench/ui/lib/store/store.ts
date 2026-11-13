import { configureStore } from "@reduxjs/toolkit";
import { setupListeners } from "@reduxjs/toolkit/query";
import { benchApi } from "./apis/benchApi";

// Only RTK Query lives in Redux. Screen state goes in the URL (nuqs) first, then local state.
export const store = configureStore({
	reducer: {
		[benchApi.reducerPath]: benchApi.reducer,
	},
	middleware: (getDefaultMiddleware) => getDefaultMiddleware().concat(benchApi.middleware),
	devTools: import.meta.env.DEV,
});

// Lets refetchOnFocus / refetchOnReconnect and skipPollingIfUnfocused work, as in ui/.
setupListeners(store.dispatch);

export type RootState = ReturnType<typeof store.getState>;

export type AppDispatch = typeof store.dispatch;