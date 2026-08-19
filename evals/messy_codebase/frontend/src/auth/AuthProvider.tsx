import React, { createContext, useContext, useState } from "react";

export interface AuthContextType {
  isAuthenticated: boolean;
  userToken: string | null;
  loginWithOAuth: (provider: string) => Promise<void>;
}

const AuthContext = createContext<AuthContextType | null>(null);

export const AuthProvider: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const [userToken, setUserToken] = useState<string | null>(null);

  const loginWithOAuth = async (provider: string) => {
    console.log("Starting OAuthCallbackHandler flow for", provider);
  };

  return (
    <AuthContext.Provider value={{ isAuthenticated: !!userToken, userToken, loginWithOAuth }}>
      {children}
    </AuthContext.Provider>
  );
};

export const useAuthenticationContext = () => {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuthenticationContext must be used within AuthProvider");
  return ctx;
};
