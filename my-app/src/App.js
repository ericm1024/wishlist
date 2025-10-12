import { useState, useEffect } from 'react';


function MyComponent() {
  const [data, setData] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
    
  useEffect(() => {
      const fetchData = async () => {
          try {
              const response = await fetch('/api/users'); // Replace with your API endpoint
              if (!response.ok) {
                  throw new Error(`HTTP error! status: ${response.status}`);
              }
              setData(await response.text());
          } catch (error) {
              setError(error);
          } finally {
              setLoading(false);
          }
      };
      
      fetchData();
  }, []); // Empty dependency array ensures this runs only once on mount

  if (loading) return <p>Loading data...</p>;
  if (error) return <p>Error: {error.message}</p>;
    
  return (
    <div>
      <h1>Fetched Data:</h1>
      {/* Render your data here */}
          <pre>{data}</pre>
    </div>
  );
}

function MyButton({count, onClick}) {

  return (
    <button onClick={onClick}>
          Clicked {count} times
    </button>
  );
}

function Login() {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [loginResponse, setLoginResponse] = useState('');  

  function handleUsername(event) {
    setUsername(event.target.value);
  };

  function handlePassword(event) {
    setPassword(event.target.value);
  };

  async function doLogin() {
        try {
          const response = await fetch('/api/login', {
            method: 'POST',
            headers: {
              'Content-Type': 'application/json',
            },
            body: JSON.stringify({
              "username": username,
              "password": password
            })
          });

          if (!response.ok) {
            throw new Error(`HTTP error! status: ${response.status}`);
          }

          const data = await response.json();
          setLoginResponse('Data sent successfully: ' + JSON.stringify(data));
        } catch (error) {
          setLoginResponse('Error sending data: ' + error.message);
        }

    console.log("username: ", username, ", password: ", password)
  }

    return (
            <div>
            <h1> Login </h1>
            Username <br/>
            <input
              type="text"
              name="username"
              onChange={handleUsername}
            /> <br/>
            Password <br/>
            <input
              type="password"
              name="user_password"
              onChange={handlePassword}              
            /> <br/>
            <button onClick={doLogin}>
              Submit
            </button>
            {loginResponse && <p>{loginResponse}</p>}
            </div>
  );
}

export default function MyApp() {
  const [count, setCount] = useState(0);
    
  function handleClick() {
      setCount(count + 1)
  }

    
  return (
    <div>
      <h1>Welcome to my app</h1>
      <MyButton count={count} onClick={handleClick} />      
          <MyButton count={count} onClick={handleClick} />
          <MyComponent />
          <Login />          
    </div>
  );
}
